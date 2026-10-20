package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/cpanato/berlin-transit/internal/config"
)

// Probe the existing local readiness route without constructing a VBB client,
// collecting departures, or invoking an output adapter. Scratch needs no shell.
func healthcheck(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err == nil && host == "0.0.0.0" {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	if !config.LoopbackAddr(addr) {
		return errors.New("invalid healthcheck address")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 1, MaxResponseHeaderBytes: 4096, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
	if err != nil {
		return errors.New("service not ready")
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("service not ready")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	var result struct {
		Status string `json:"status"`
	}
	if err != nil || len(body) > 1024 || resp.StatusCode != http.StatusOK || json.Unmarshal(body, &result) != nil || result.Status != "ready" {
		return errors.New("service not ready")
	}
	return nil
}
