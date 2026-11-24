// Package gateway assembles the first milestone's immutable routes and proxy pools.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"aegisgate/internal/config"
)

type pool struct {
	nodes []*httputil.ReverseProxy
	next  atomic.Uint64
}

func (p *pool) pick() *httputil.ReverseProxy {
	index := p.next.Add(1) - 1
	return p.nodes[index%uint64(len(p.nodes))]
}

type route struct {
	id     string
	prefix string
	pool   *pool
}
type Gateway struct {
	routes    []route
	transport *http.Transport
	settings  config.Server
	logger    *slog.Logger
}
type requestIDKey struct{}

func New(c config.Config, logger *slog.Logger) (*Gateway, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Explicit configuration: do not accidentally forward through the host's proxy environment.
	tr.Proxy = nil
	tr.DialContext = (&net.Dialer{Timeout: c.Transport.DialTimeout, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = c.Transport.DialTimeout
	tr.ResponseHeaderTimeout = c.Transport.ResponseHeaderTimeout
	tr.IdleConnTimeout = c.Transport.IdleConnTimeout
	tr.MaxIdleConns = c.Transport.MaxIdleConns
	tr.MaxIdleConnsPerHost = c.Transport.MaxIdleConnsPerHost
	tr.MaxConnsPerHost = c.Transport.MaxConnsPerHost
	g := &Gateway{transport: tr, settings: c.Server, logger: logger}
	pools := make(map[string]*pool, len(c.Upstreams))
	for name, nodes := range c.Upstreams {
		p := &pool{}
		for _, raw := range nodes {
			target, _ := config.Origin(raw) // Already validated above.
			proxy := &httputil.ReverseProxy{
				Transport: tr,
				Rewrite: func(pr *httputil.ProxyRequest) {
					pr.SetURL(target)
					for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
						pr.Out.Header.Del(h)
					}
					pr.SetXForwarded()
					pr.Out.Header.Set("X-Request-ID", pr.In.Context().Value(requestIDKey{}).(string))
				},
				ModifyResponse: func(resp *http.Response) error {
					resp.Header.Set("X-Request-ID", resp.Request.Context().Value(requestIDKey{}).(string))
					return nil
				},
				ErrorHandler: g.proxyError,
				ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelError),
			}
			p.nodes = append(p.nodes, proxy)
		}
		pools[name] = p
	}
	for _, r := range c.Routes {
		g.routes = append(g.routes, route{id: r.ID, prefix: r.PathPrefix, pool: pools[r.Upstream]})
	}
	sort.Slice(g.routes, func(i, j int) bool { return len(g.routes[i].prefix) > len(g.routes[j].prefix) })
	return g, nil
}

func (g *Gateway) Close() { g.transport.CloseIdleConnections() }

func (g *Gateway) Server() *http.Server {
	return &http.Server{
		Addr: g.settings.Listen, Handler: g, ReadHeaderTimeout: g.settings.ReadHeaderTimeout,
		ReadTimeout: g.settings.ReadTimeout, WriteTimeout: g.settings.WriteTimeout,
		IdleTimeout: g.settings.IdleTimeout, MaxHeaderBytes: 1 << 20,
		ErrorLog: slog.NewLogLogger(g.logger.Handler(), slog.LevelError),
	}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	id := hex.EncodeToString(bytes[:])
	w.Header().Set("X-Request-ID", id)
	response := &statusWriter{ResponseWriter: w}
	routeID := "_unmatched"
	defer func() {
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		g.logger.Info("request", "request_id", id, "method", r.Method,
			"route", routeID, "status", status, "bytes", response.bytes,
			"duration_ms", float64(time.Since(start).Microseconds())/1000)
	}()
	if r.URL.Path == "/healthz" {
		routeID = "_health"
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = response.Write([]byte("{\"status\":\"ok\"}\n"))
		}
		return
	}
	for _, rt := range g.routes {
		if !matches(rt.prefix, r.URL.Path) {
			continue
		}
		routeID = rt.id
		if r.ContentLength > g.settings.MaxBodyBytes {
			http.Error(response, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		// Streaming limit: unknown-length bodies may send a prefix upstream before rejection.
		r.Body = http.MaxBytesReader(response, r.Body, g.settings.MaxBodyBytes)
		ctx, cancel := context.WithTimeout(r.Context(), g.settings.RequestTimeout)
		defer cancel()
		ctx = context.WithValue(ctx, requestIDKey{}, id)
		rt.pool.pick().ServeHTTP(response, r.WithContext(ctx))
		return
	}
	http.NotFound(response, r)
}

// Prefixes match on path-segment boundaries: /api also matches /api/users, but not /apix.
func matches(prefix, path string) bool {
	return prefix == "/" || path == prefix || strings.HasPrefix(path, prefix+"/")
}

func (g *Gateway) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadGateway
	message := "bad gateway"
	var tooLarge *http.MaxBytesError
	var netErr net.Error
	switch {
	case errors.As(err, &tooLarge):
		status, message = http.StatusRequestEntityTooLarge, "request body too large"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		status, message = http.StatusGatewayTimeout, "gateway timeout"
	}
	// Do not include the full request URL or headers in diagnostics.
	g.logger.Warn("proxy failed", "status", status)
	http.Error(w, message, status)
}

// Unwrap preserves ResponseController access to the underlying writer (e.g. flushing).
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}
