// Package mockupstream supplies a bounded HTTP echo service for local experiments.
package mockupstream

import (
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

type Options struct {
	Name      string
	Delay     time.Duration
	ErrorRate float64
}

func Handler(o Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", o.Name)
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		timer := time.NewTimer(o.Delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "body read failed or exceeds 4 MiB", http.StatusRequestEntityTooLarge)
			return
		}
		if rand.Float64() < o.ErrorRate {
			http.Error(w, "simulated upstream failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Upstream     string `json:"upstream"`
			Method       string `json:"method"`
			Path         string `json:"path"`
			Query        string `json:"query"`
			Body         string `json:"body"`
			RequestID    string `json:"request_id"`
			ForwardedFor string `json:"forwarded_for"`
		}{o.Name, r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Get("X-Request-ID"), r.Header.Get("X-Forwarded-For")})
	})
}
