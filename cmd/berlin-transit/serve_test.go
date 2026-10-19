package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/config"
	"github.com/cpanato/berlin-transit/internal/httpapi"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/service"
)

func TestServeLifecycle(t *testing.T) {
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := service.New(ctx, &fakeProvider{}, cfg.Service)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := observability.Logger(&logs, slog.LevelInfo)
	a := httpapi.New(s, log, false, listener.Addr().String())
	server := httpapi.Server(listener.Addr().String(), a, time.Second, log)
	done := make(chan error, 1)
	go func() { done <- runServer(ctx, server, listener, a, log) }()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + listener.Addr().String() + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown hung")
	}
	if !strings.Contains(logs.String(), "server_started") || !strings.Contains(logs.String(), "server_stopped") {
		t.Fatal(logs.String())
	}
	if resp, err := client.Get("http://" + listener.Addr().String() + "/healthz"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("listener remained open")
	}
}

func TestServeErrorsAndArguments(t *testing.T) {
	log := observability.Logger(io.Discard, slog.LevelInfo)
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(ctx, cfg, &fakeProvider{}, log); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := serve(context.Background(), config.Config{}, &fakeProvider{}, log); err == nil {
		t.Fatal("invalid service config accepted")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	cfg.ListenAddr = l.Addr().String()
	if err := serve(context.Background(), cfg, &fakeProvider{}, log); err == nil || err.Error() != "listener unavailable" {
		t.Fatalf("%v", err)
	}
	if err := run(context.Background(), []string{"serve", "-unknown", "secret"}, io.Discard, func(string) string { return "" }); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("%v", err)
	}
	if err := run(ctx, []string{"serve"}, io.Discard, func(string) string { return "" }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Close()
	a := httpapi.New(nil, log, false, l2.Addr().String())
	server := httpapi.Server(l2.Addr().String(), a, time.Second, log)
	if err := runServer(context.Background(), server, l2, a, log); err == nil || err.Error() != "server unavailable" {
		t.Fatal(err)
	}
}
