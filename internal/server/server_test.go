package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownDrainsInFlight(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		if r.Context().Err() != nil {
			t.Error("request canceled by shutdown")
		}
		_, _ = io.WriteString(w, "completed")
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, listener, srv, 2*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	result := make(chan error, 1)
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			raw, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			err = readErr
			if string(raw) != "completed" {
				err = errors.New("incomplete response")
			}
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request never started")
	}
	cancel()
	select {
	case err := <-served:
		t.Fatalf("returned before drain: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
func TestShutdownDeadlineClosesConnection(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, listener, srv, 40*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := client.Get("http://" + listener.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	cancel()
	if err := <-served; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v want deadline", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request not canceled after forced close")
	}
	<-done
}
