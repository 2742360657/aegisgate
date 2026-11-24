// Package config loads a small, validated configuration before the listener opens.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Server struct {
	Listen            string        `yaml:"listen"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	RequestTimeout    time.Duration `yaml:"request_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	MaxBodyBytes      int64         `yaml:"max_body_bytes"`
}
type Transport struct {
	DialTimeout           time.Duration `yaml:"dial_timeout"`
	ResponseHeaderTimeout time.Duration `yaml:"response_header_timeout"`
	IdleConnTimeout       time.Duration `yaml:"idle_conn_timeout"`
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost   int           `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost       int           `yaml:"max_conns_per_host"`
}
type Route struct {
	ID         string `yaml:"id"`
	PathPrefix string `yaml:"path_prefix"`
	Upstream   string `yaml:"upstream"`
}
type Config struct {
	Server    Server              `yaml:"server"`
	Transport Transport           `yaml:"transport"`
	Upstreams map[string][]string `yaml:"upstreams"`
	Routes    []Route             `yaml:"routes"`
}

func Default() Config {
	return Config{
		Server: Server{
			Listen: "127.0.0.1:8080", ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
			RequestTimeout: 20 * time.Second, ShutdownTimeout: 25 * time.Second, MaxBodyBytes: 4 << 20,
		},
		Transport: Transport{
			DialTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 60 * time.Second,
			MaxIdleConns: 128, MaxIdleConnsPerHost: 32, MaxConnsPerHost: 128,
		},
	}
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

func Decode(r io.Reader) (Config, error) {
	const maxConfigBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(r, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(raw) > maxConfigBytes {
		return Config{}, errors.New("config exceeds 1 MiB")
	}
	c := Default()
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("config must contain exactly one YAML document")
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Origin accepts only an HTTP(S) origin. Path rewriting is deliberately deferred.
func Origin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid upstream URL")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("upstream must be an http(s) origin without credentials, path, query or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid upstream port")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("empty upstream port")
	}
	u.Path = ""
	return u, nil
}

func (c Config) Validate() error {
	_, listenPort, err := net.SplitHostPort(c.Server.Listen)
	if err != nil {
		return fmt.Errorf("server.listen: %w", err)
	}
	port, err := strconv.Atoi(listenPort)
	if err != nil || port < 0 || port > 65535 {
		return errors.New("server.listen must have a numeric port in [0,65535]")
	}
	for name, d := range map[string]time.Duration{
		"read_header_timeout": c.Server.ReadHeaderTimeout, "read_timeout": c.Server.ReadTimeout,
		"write_timeout": c.Server.WriteTimeout, "idle_timeout": c.Server.IdleTimeout,
		"request_timeout": c.Server.RequestTimeout, "shutdown_timeout": c.Server.ShutdownTimeout,
		"dial_timeout": c.Transport.DialTimeout, "response_header_timeout": c.Transport.ResponseHeaderTimeout,
		"idle_conn_timeout": c.Transport.IdleConnTimeout,
	} {
		if d <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if c.Server.MaxBodyBytes <= 0 {
		return errors.New("max_body_bytes must be positive")
	}
	if c.Transport.MaxIdleConns <= 0 || c.Transport.MaxIdleConnsPerHost <= 0 || c.Transport.MaxConnsPerHost <= 0 {
		return errors.New("connection pool limits must be positive")
	}
	if len(c.Upstreams) == 0 || len(c.Routes) == 0 {
		return errors.New("at least one upstream and route required")
	}
	for name, nodes := range c.Upstreams {
		if strings.TrimSpace(name) == "" || len(nodes) == 0 {
			return errors.New("upstream name and nodes required")
		}
		seen := make(map[string]bool)
		for _, raw := range nodes {
			u, err := Origin(raw)
			if err != nil {
				return fmt.Errorf("upstream %q: %w", name, err)
			}
			if seen[u.String()] {
				return fmt.Errorf("duplicate node in upstream %q", name)
			}
			seen[u.String()] = true
		}
	}
	ids, paths := make(map[string]bool), make(map[string]bool)
	for _, route := range c.Routes {
		if strings.TrimSpace(route.ID) == "" || ids[route.ID] {
			return errors.New("route IDs must be nonempty and unique")
		}
		ids[route.ID] = true
		p := route.PathPrefix
		if p == "" || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") ||
			(len(p) > 1 && strings.HasSuffix(p, "/")) || paths[p] {
			return errors.New("route prefixes must be unique absolute paths without query, fragment or trailing slash (except /)")
		}
		paths[p] = true
		if _, ok := c.Upstreams[route.Upstream]; !ok {
			return fmt.Errorf("route %q references missing upstream", route.ID)
		}
	}
	return nil
}
