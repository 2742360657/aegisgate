package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"aegisgate/internal/config"
	"aegisgate/internal/gateway"
	"aegisgate/internal/server"
)

func run() error {
	path := flag.String("config", "configs/gateway.yaml", "YAML configuration file")
	check := flag.Bool("check", false, "validate configuration and exit")
	flag.Parse()
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *check {
		fmt.Println("configuration valid")
		return nil
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	g, err := gateway.New(c, logger)
	if err != nil {
		return err
	}
	defer g.Close()
	listener, err := net.Listen("tcp", c.Server.Listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("gateway listening", "address", listener.Addr().String())
	return server.Serve(ctx, listener, g.Server(), c.Server.ShutdownTimeout, logger)
}
func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
