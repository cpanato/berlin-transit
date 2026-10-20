package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cpanato/berlin-transit/internal/collector"
	"github.com/cpanato/berlin-transit/internal/config"
	"github.com/cpanato/berlin-transit/internal/httpapi"
	"github.com/cpanato/berlin-transit/internal/integrations/trmnl"
	"github.com/cpanato/berlin-transit/internal/service"
)

func serve(ctx context.Context, cfg config.Config, p service.Provider, log *slog.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s, err := service.New(ctx, p, cfg.Service)
	if err != nil {
		return err
	}
	defer s.Close()
	var publisher *trmnl.Client
	if cfg.TRMNL.Enabled {
		publisher, err = trmnl.NewClient(cfg.TRMNL)
		if err != nil {
			return err
		}
		defer publisher.Close()
	}
	network := "tcp"
	if host, _, _ := net.SplitHostPort(cfg.ListenAddr); host == "0.0.0.0" {
		// Keep the explicit container exception IPv4-only; tcp can select a
		// dual-stack IPv6 wildcard even when given an IPv4 unspecified address.
		network = "tcp4"
	}
	listener, err := net.Listen(network, cfg.ListenAddr)
	if err != nil {
		return errors.New("listener unavailable")
	}
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	if cfg.Collector.Enabled {
		workers.Go(func() { collector.Run(ctx, cfg.Collector, s, log) })
	}
	if cfg.TRMNL.Enabled {
		workers.Go(func() { trmnl.Run(ctx, cfg.TRMNL, publisher, s, cfg.Collector.Stations, cfg.Collector.Options, log) })
	}
	a := httpapi.New(s, log, cfg.MetricsEnabled, listener.Addr().String())
	server := httpapi.Server(listener.Addr().String(), a, cfg.HTTPTimeout, log)
	return runServer(ctx, server, listener, a, log)
}

func runServer(ctx context.Context, server *http.Server, listener net.Listener, a *httpapi.API, log *slog.Logger) error {
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	log.Info("server_started", "listen_addr", listener.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("server unavailable")
	case <-ctx.Done():
		a.NotReady()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdown)
		if shutdownErr != nil {
			_ = server.Close()
		}
		serveErr := <-done
		if shutdownErr != nil {
			return errors.New("server shutdown timeout")
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return errors.New("server unavailable")
		}
		log.Info("server_stopped")
		return nil
	}
}
