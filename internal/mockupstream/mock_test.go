package mockupstream

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthySuccessAndFailure(t *testing.T) {
	for _, rate := range []float64{0, 1} {
		handler := Handler(Options{Name: "test", ErrorRate: rate})
		health := httptest.NewRecorder()
		handler.ServeHTTP(health, httptest.NewRequest("GET", "/healthz", nil))
		if health.Code != 200 {
			t.Fatalf("health returned %d", health.Code)
		}
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, httptest.NewRequest("POST", "/echo", strings.NewReader("hello")))
		want := 200
		if rate == 1 {
			want = 500
		}
		if result.Code != want {
			t.Fatalf("error rate %f: %d", rate, result.Code)
		}
	}
}
func TestDelayHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	start := time.Now()
	Handler(Options{Delay: time.Hour}).ServeHTTP(httptest.NewRecorder(), req)
	if time.Since(start) > time.Second {
		t.Fatal("canceled request waited for delay")
	}
}
