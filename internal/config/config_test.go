package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	c, err := Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:8080" || c.MetricsEnabled || c.HTTPTimeout != 10*time.Second || c.LogLevel != slog.LevelInfo || c.Service.Timeout != c.HTTPTimeout || c.Service.RequestsPerMinute != 30 || c.Service.CacheEntries != 256 || c.Service.CacheBytes != 16<<20 || c.Service.MaxStale != 5*time.Minute || c.Service.StationsTTL != 24*time.Hour || c.Service.DeparturesTTL != 30*time.Second {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	env := map[string]string{"BERLIN_TRANSIT_LISTEN_ADDR": "[::1]:8081", "BERLIN_TRANSIT_HTTP_TIMEOUT": "2s", "BERLIN_TRANSIT_DEPARTURES_TTL": "1s", "BERLIN_TRANSIT_STATIONS_TTL": "1h", "BERLIN_TRANSIT_MAX_STALE": "0s", "BERLIN_TRANSIT_METRICS_ENABLED": "true", "BERLIN_TRANSIT_LOG_LEVEL": "debug", "BERLIN_TRANSIT_CACHE_ENTRIES": "10", "BERLIN_TRANSIT_CACHE_BYTES": "1048576", "BERLIN_TRANSIT_REQUESTS_PER_MINUTE": "10"}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "[::1]:8081" || !c.MetricsEnabled || c.HTTPTimeout != 2*time.Second || c.LogLevel != slog.LevelDebug || c.Service.MaxStale != 0 || c.Service.CacheEntries != 10 || c.Service.CacheBytes != 1<<20 || c.Service.RequestsPerMinute != 10 {
		t.Fatalf("%+v", c)
	}
}

func TestRejectedConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"LISTEN_ADDR", "0.0.0.0:8080"}, {"LISTEN_ADDR", ":8080"}, {"LISTEN_ADDR", "[::]:8080"}, {"LISTEN_ADDR", "192.168.1.2:8080"}, {"LISTEN_ADDR", "localhost:8080"}, {"LISTEN_ADDR", "127.0.0.1:0"}, {"LISTEN_ADDR", "127.0.0.1:70000"}, {"LISTEN_ADDR", "127.0.0.1:no"}, {"LISTEN_ADDR", "invalid"},
		{"VBB_BASE_URL", "https://private.example/secret"}, {"HTTP_TIMEOUT", "secret"}, {"HTTP_TIMEOUT", "0s"}, {"HTTP_TIMEOUT", "2m"},
		{"DEPARTURES_TTL", "0s"}, {"DEPARTURES_TTL", "6m"}, {"STATIONS_TTL", "-1s"}, {"STATIONS_TTL", "200h"}, {"MAX_STALE", "-1s"}, {"MAX_STALE", "25h"},
		{"CACHE_ENTRIES", "0"}, {"CACHE_ENTRIES", "4097"}, {"CACHE_BYTES", "0"}, {"CACHE_BYTES", "67108865"}, {"REQUESTS_PER_MINUTE", "secret"}, {"REQUESTS_PER_MINUTE", "61"}, {"REQUESTS_PER_MINUTE", "0"}, {"METRICS_ENABLED", "secret"}, {"LOG_LEVEL", "secret"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			name := "BERLIN_TRANSIT_" + tc.name
			_, err := Load(func(k string) string {
				if k == name {
					return tc.value
				}
				return ""
			})
			if err == nil || err.Error() != "invalid "+name || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
