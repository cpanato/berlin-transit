package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{"ready", 200, `{"status":"ready"}`, true}, {"unready", 503, `{"status":"not_ready"}`, false}, {"wrong body", 200, `{"status":"ok"}`, false},
		{"malformed", 200, "private raw body", false}, {"oversized", 200, strings.Repeat("x", 1025), false}, {"redirect", 302, `{"status":"ready"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/readyz" {
					t.Error("unexpected probe request")
				}
				w.Header().Set("Location", "http://127.0.0.1:1/private")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			addr := server.Listener.Addr().String()
			err := healthcheck(context.Background(), addr)
			if (err == nil) != tc.valid || calls != 1 {
				t.Fatalf("%v, calls=%d", err, calls)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("probe leaked response")
			}
			// A wildcard inside the container must target loopback, never 0.0.0.0.
			_, port, _ := net.SplitHostPort(addr)
			err = run(context.Background(), []string{"healthcheck"}, io.Discard, func(k string) string {
				switch k {
				case "BERLIN_TRANSIT_LISTEN_ADDR":
					return "0.0.0.0:" + port
				case "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN":
					return "true"
				}
				return ""
			})
			if (err == nil) != tc.valid {
				t.Fatal("container normalization", err)
			}
		})
	}
	for _, addr := range []string{"http://private.example/secret", "192.168.1.2:8080", "example.com:80", "invalid"} {
		if err := healthcheck(context.Background(), addr); err == nil {
			t.Fatal("unsafe target")
		}
	}
	if err := run(context.Background(), []string{"healthcheck", "secret"}, io.Discard, func(string) string { return "" }); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid arguments")
	}
}

func TestHealthcheckCancellationAndConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := healthcheck(ctx, server.Listener.Addr().String()); err == nil {
		t.Fatal("cancelled request accepted")
	}
	addr := server.Listener.Addr().String()
	server.Close()
	if err := healthcheck(context.Background(), addr); err == nil {
		t.Fatal("closed service healthy")
	}
}
