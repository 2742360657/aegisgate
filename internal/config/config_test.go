package config

import (
	"strings"
	"testing"
)

const valid = `upstreams:
  demo: [http://127.0.0.1:18081]
routes:
  - id: demo
    path_prefix: /
    upstream: demo
`

func TestDecodeDefaultsAndDurations(t *testing.T) {
	c, err := Decode(strings.NewReader("server:\n  request_timeout: 7s\n" + valid))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.RequestTimeout.Seconds() != 7 || c.Server.MaxBodyBytes != 4<<20 {
		t.Fatalf("unexpected defaults: %+v", c.Server)
	}
}
func TestRejectInvalidConfig(t *testing.T) {
	cases := map[string]string{
		"bad listen port":    "server:\n  listen: 127.0.0.1:99999\n" + valid,
		"named listen port":  "server:\n  listen: 127.0.0.1:http\n" + valid,
		"unknown key":        "typo: true\n" + valid,
		"unknown nested key": "server:\n  tiemout: 5s\n" + valid,
		"zero timeout":       "server:\n  request_timeout: 0s\n" + valid,
		"bad duration":       "server:\n  request_timeout: potato\n" + valid,
		"negative limit":     "server:\n  max_body_bytes: -1\n" + valid,
		"zero connections":   "transport:\n  max_conns_per_host: 0\n" + valid,
		"second document":    valid + "---\nhello: world\n",
		"empty":              "",
		"missing upstream":   strings.Replace(valid, "upstream: demo", "upstream: absent", 1),
		"empty pool":         strings.Replace(valid, "[http://127.0.0.1:18081]", "[]", 1),
		"bad prefix":         strings.Replace(valid, "path_prefix: /", "path_prefix: /api/", 1),
		"duplicate route":    valid + "  - id: demo\n    path_prefix: /other\n    upstream: demo\n",
		"duplicate prefix":   valid + "  - id: other\n    path_prefix: /\n    upstream: demo\n",
		"duplicate node":     strings.Replace(valid, "[http://127.0.0.1:18081]", "[http://127.0.0.1:18081, http://127.0.0.1:18081/]", 1),
		"oversize config":    strings.Repeat(" ", (1<<20)+1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(raw)); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
func TestOriginValidation(t *testing.T) {
	for _, raw := range []string{
		"ftp://localhost", "http://", "http://user:secret@localhost",
		"http://localhost/api", "http://localhost?x=1", "http://localhost?",
		"http://localhost#fragment", "http://localhost:70000", "http://localhost:",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Origin(raw); err == nil {
				t.Fatal("unsafe or unsupported origin accepted")
			}
		})
	}
	for _, raw := range []string{"http://127.0.0.1:8080", "https://example.com/", "http://[::1]:8080"} {
		if _, err := Origin(raw); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
