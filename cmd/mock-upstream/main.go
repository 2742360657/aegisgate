package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aegisgate/internal/mockupstream"
	"aegisgate/internal/server"
)

func run() error {
	addr := flag.String("listen", "127.0.0.1:18081", "listen address")
	name := flag.String("name", "node-a", "node name")
	delay := flag.Duration("delay", 0, "response delay, e.g. 500ms")
	errorRate := flag.Float64("error-rate", 0, "random failure probability in [0,1]")
	flag.Parse()
	if *delay < 0 || *errorRate < 0 || *errorRate > 1 {
		return errors.New("delay must be nonnegative and error-rate must be in [0,1]")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	srv := &http.Server{
		Addr: *addr, Handler: mockupstream.Handler(mockupstream.Options{Name: *name, Delay: *delay, ErrorRate: *errorRate}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("mock listening", "name", *name, "address", listener.Addr().String())
	return server.Serve(ctx, listener, srv, 25*time.Second, logger)
}
func main() {
	if err := run(); err != nil {
		slog.Error("mock stopped", "error", err)
		os.Exit(1)
	}
}
