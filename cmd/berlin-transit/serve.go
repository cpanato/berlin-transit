package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/cpanato/berlin-transit/internal/config"
	"github.com/cpanato/berlin-transit/internal/httpapi"
	"github.com/cpanato/berlin-transit/internal/service"
)

func serve(ctx context.Context, cfg config.Config, p service.Provider, log *slog.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := service.New(ctx, p, cfg.Service)
	if err != nil {
		return err
	}
	defer s.Close()
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return errors.New("listener unavailable")
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
