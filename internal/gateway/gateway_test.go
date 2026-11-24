package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aegisgate/internal/config"
)

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func setup(t *testing.T, c config.Config) (*Gateway, *httptest.Server) {
	t.Helper()
	g, err := New(c, logger())
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(g)
	front.Client().Timeout = 3 * time.Second
	t.Cleanup(func() { front.Close(); g.Close() })
	return g, front
}
func settings(nodes ...string) config.Config {
	c := config.Default()
	c.Upstreams = map[string][]string{"demo": nodes}
	c.Routes = []config.Route{{ID: "demo", PathPrefix: "/", Upstream: "demo"}}
	return c
}
func request(t *testing.T, client *http.Client, method, target string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}
func TestRoundRobinAndConcurrentRequests(t *testing.T) {
	var a, b atomic.Int64
	node := func(counter *atomic.Int64, name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			_, _ = io.WriteString(w, name)
		}))
	}
	first, second := node(&a, "a"), node(&b, "b")
	defer first.Close()
	defer second.Close()
	_, front := setup(t, settings(first.URL, second.URL))
	for _, want := range []string{"a", "b", "a", "b"} {
		resp, raw := request(t, front.Client(), "GET", front.URL+"/", nil)
		if resp.StatusCode != 200 || string(raw) != want {
			t.Fatalf("got %d %q, want %q", resp.StatusCode, raw, want)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := front.Client().Get(front.URL + "/")
			if err != nil {
				t.Error(err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Errorf("status=%d error=%v", resp.StatusCode, err)
			}
		}()
	}
	wg.Wait()
	if a.Load() != 42 || b.Load() != 42 {
		t.Fatalf("unbalanced: %d / %d", a.Load(), b.Load())
	}
}
func TestForwardingAndTrustBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Request-ID", "upstream-spoof")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method, "path": r.URL.Path, "query": r.URL.RawQuery, "body": string(body),
			"xff": r.Header.Get("X-Forwarded-For"), "proto": r.Header.Get("X-Forwarded-Proto"),
			"forwarded": r.Header.Get("Forwarded"), "request_id": r.Header.Get("X-Request-ID"),
			"hop": r.Header.Get("X-Hop"),
		})
	}))
	defer upstream.Close()
	_, front := setup(t, settings(upstream.URL))
	req, _ := http.NewRequest("POST", front.URL+"/api/echo?q=hello%20world", strings.NewReader("hello"))
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Forwarded", "for=1.2.3.4")
	req.Header.Set("X-Request-ID", "client-spoof")
	req.Header.Set("Connection", "X-Hop, X-Request-ID")
	req.Header.Set("X-Hop", "secret")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["method"] != "POST" || got["path"] != "/api/echo" || got["query"] != "q=hello%20world" || got["body"] != "hello" {
		t.Fatalf("forwarding changed: %+v", got)
	}
	if got["xff"] != "127.0.0.1" || got["proto"] != "http" || got["forwarded"] != "" || got["hop"] != "" {
		t.Fatalf("untrusted headers forwarded: %+v", got)
	}
	if len(got["request_id"]) != 32 || got["request_id"] != resp.Header.Get("X-Request-ID") {
		t.Fatalf("request ID not generated consistently: %+v", got)
	}
}
func TestLongestPrefixAndSegmentBoundary(t *testing.T) {
	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "root") }))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "api") }))
	defer root.Close()
	defer api.Close()
	c := settings(root.URL)
	c.Upstreams["api"] = []string{api.URL}
	c.Routes = append(c.Routes, config.Route{ID: "api", PathPrefix: "/api", Upstream: "api"})
	_, front := setup(t, c)
	for path, want := range map[string]string{"/": "root", "/api": "api", "/api/users": "api", "/apix": "root"} {
		_, raw := request(t, front.Client(), "GET", front.URL+path, nil)
		if string(raw) != want {
			t.Errorf("%s got %q want %q", path, raw, want)
		}
	}
	c.Routes = c.Routes[1:]
	_, limited := setup(t, c)
	resp, _ := request(t, limited.Client(), "GET", limited.URL+"/missing", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("missing route = %d", resp.StatusCode)
	}
}
func TestBodyLimitsKnownAndChunked(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	c := settings(upstream.URL)
	c.Server.MaxBodyBytes = 8
	_, front := setup(t, c)
	for _, body := range []io.Reader{strings.NewReader("12345678"), struct{ io.Reader }{strings.NewReader("12345678")}} {
		resp, _ := request(t, front.Client(), "POST", front.URL+"/", body)
		if resp.StatusCode != 200 {
			t.Fatalf("body at limit = %d", resp.StatusCode)
		}
	}
	before := calls.Load()
	resp, _ := request(t, front.Client(), "POST", front.URL+"/", strings.NewReader("123456789"))
	if resp.StatusCode != 413 || calls.Load() != before {
		t.Fatalf("known oversize: status=%d upstream calls=%d", resp.StatusCode, calls.Load()-before)
	}
	resp, _ = request(t, front.Client(), "POST", front.URL+"/", struct{ io.Reader }{strings.NewReader("123456789")})
	if resp.StatusCode != 413 {
		t.Fatalf("chunked oversize = %d", resp.StatusCode)
	}
}
func TestUpstreamFailuresAndTimeouts(t *testing.T) {
	for _, mode := range []string{"closed", "headers", "request", "upstream500"} {
		t.Run(mode, func(t *testing.T) {
			node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "upstream500" {
					w.WriteHeader(500)
					return
				}
				<-r.Context().Done()
			}))
			defer node.Close()
			c := settings(node.URL)
			want := http.StatusGatewayTimeout
			switch mode {
			case "closed":
				node.Close()
				want = 502
			case "headers":
				c.Transport.ResponseHeaderTimeout = 40 * time.Millisecond
			case "request":
				c.Server.RequestTimeout = 40 * time.Millisecond
			case "upstream500":
				want = 500
			}
			_, front := setup(t, c)
			resp, _ := request(t, front.Client(), "GET", front.URL+"/", nil)
			if resp.StatusCode != want {
				t.Fatalf("got %d want %d", resp.StatusCode, want)
			}
		})
	}
}
func TestClientCancellationReachesUpstream(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer node.Close()
	_, front := setup(t, settings(node.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", front.URL+"/", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := front.Client().Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not reached")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation not propagated")
	}
	<-done
}
func TestHealthAndAccessLogPrivacy(t *testing.T) {
	c := settings("http://127.0.0.1:1")
	var logs bytes.Buffer
	g, err := New(c, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://gateway/healthz?secret=private-query", nil)
	r.Header.Set("Authorization", "private-token")
	g.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(logs.String(), "private") {
		t.Fatalf("status=%d log=%s", w.Code, logs.String())
	}
}
