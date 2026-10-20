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
	if c.ListenAddr != "127.0.0.1:8080" || c.AllowContainerListen || c.MetricsEnabled || c.HTTPTimeout != 10*time.Second || c.LogLevel != slog.LevelInfo || c.Service.Timeout != c.HTTPTimeout || c.Service.RequestsPerMinute != 30 || c.Service.CacheEntries != 256 || c.Service.CacheBytes != 16<<20 || c.Service.MaxStale != 5*time.Minute || c.Service.StationsTTL != 24*time.Hour || c.Service.DeparturesTTL != 30*time.Second {
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

func TestBackgroundConfiguration(t *testing.T) {
	env := map[string]string{"BERLIN_TRANSIT_COLLECTOR_ENABLED": "true", "BERLIN_TRANSIT_COLLECTOR_STATIONS": "a,b", "BERLIN_TRANSIT_COLLECTOR_INTERVAL": "1m", "BERLIN_TRANSIT_COLLECTOR_MODE": "bus", "BERLIN_TRANSIT_COLLECTOR_LIMIT": "5", "BERLIN_TRANSIT_COLLECTOR_DURATION": "15", "BERLIN_TRANSIT_COLLECTOR_DIRECTION": "dest", "TRMNL_ENABLED": "true", "TRMNL_WEBHOOK_URL": "https://trmnl.com/api/custom_plugins/test-token", "TRMNL_INTERVAL": "10m", "TRMNL_MAX_DEPARTURES": "4"}
	c, err := Load(func(k string) string { return env[k] })
	if err != nil || !c.Collector.Enabled || !c.TRMNL.Enabled || c.Collector.Interval != time.Minute || c.Collector.Options.Mode != "bus" || c.Collector.Options.Limit != 5 || c.Collector.Options.Duration != 15 || c.Collector.Options.Direction != "dest" || c.TRMNL.MaxDepartures != 4 || c.TRMNL.Interval != 10*time.Minute {
		t.Fatalf("config: %v", err)
	}
	for _, tc := range []struct{ name, value string }{
		{"BERLIN_TRANSIT_COLLECTOR_ENABLED", "invalid"}, {"TRMNL_ENABLED", "invalid"}, {"BERLIN_TRANSIT_COLLECTOR_INTERVAL", "bad"}, {"BERLIN_TRANSIT_COLLECTOR_INTERVAL", "1s"}, {"TRMNL_INTERVAL", "1m"}, {"TRMNL_MAX_DEPARTURES", "0"}, {"TRMNL_MAX_DEPARTURES", "9"}, {"BERLIN_TRANSIT_COLLECTOR_LIMIT", "bad"}, {"BERLIN_TRANSIT_COLLECTOR_DURATION", "121"}, {"BERLIN_TRANSIT_COLLECTOR_STATIONS", "a,a"}, {"BERLIN_TRANSIT_COLLECTOR_STATIONS", "a,"}, {"BERLIN_TRANSIT_COLLECTOR_STATIONS", "../"}, {"BERLIN_TRANSIT_COLLECTOR_STATIONS", strings.Repeat("x", 1285)}, {"BERLIN_TRANSIT_COLLECTOR_STATIONS", "a,b,c,d,e,f"}, {"BERLIN_TRANSIT_COLLECTOR_MODE", "unknown"}, {"BERLIN_TRANSIT_COLLECTOR_DIRECTION", "a/b"}, {"TRMNL_WEBHOOK_URL", "https://private.example/secret"}, {"BERLIN_TRANSIT_COLLECTOR_ENABLED", "false"},
	} {
		_, err := Load(func(k string) string {
			if k == tc.name {
				return tc.value
			}
			return env[k]
		})
		if err == nil || strings.Contains(err.Error(), "private.example") {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	_, err = Load(func(k string) string {
		if k == "BERLIN_TRANSIT_COLLECTOR_ENABLED" {
			return "true"
		}
		return ""
	})
	if err == nil {
		t.Fatal("empty station list")
	}
	_, err = Load(func(k string) string {
		if k == "BERLIN_TRANSIT_REQUESTS_PER_MINUTE" {
			return "1"
		}
		if k == "BERLIN_TRANSIT_COLLECTOR_INTERVAL" {
			return "30s"
		}
		return env[k]
	})
	if err == nil {
		t.Fatal("insufficient cycle budget")
	}
}

func TestContainerListenerOptIn(t *testing.T) {
	c, err := Load(func(k string) string {
		if k == "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN" {
			return "true"
		}
		return ""
	})
	if err != nil || c.ListenAddr != "127.0.0.1:8080" {
		t.Fatal("flag changed default bind", err)
	}

	for _, tc := range []struct {
		addr, flag string
		valid      bool
	}{
		{"0.0.0.0:8080", "true", true}, {"0.0.0.0:8080", "false", false}, {"0.0.0.0:8080", "", false}, {"127.0.0.1:8080", "true", true},
		{"[::]:8080", "true", false}, {"192.168.1.2:8080", "true", false}, {"8.8.8.8:8080", "true", false}, {"localhost:8080", "true", false}, {":8080", "true", false},
		{"0.0.0.0:0", "true", false}, {"0.0.0.0:65536", "true", false}, {"[::1%lo0]:8080", "true", false}, {"127.0.0.1:8080", "invalid", false},
	} {
		c, err := Load(func(k string) string {
			switch k {
			case "BERLIN_TRANSIT_LISTEN_ADDR":
				return tc.addr
			case "BERLIN_TRANSIT_ALLOW_CONTAINER_LISTEN":
				return tc.flag
			}
			return ""
		})
		if (err == nil) != tc.valid {
			t.Fatalf("%s flag=%s: %v", tc.addr, tc.flag, err)
		}
		if err == nil && !c.AllowContainerListen {
			t.Fatal("explicit flag lost")
		}
	}
	for _, addr := range []string{"0.0.0.0:8080", "[::]:8080", "example.com:80", "invalid"} {
		if LoopbackAddr(addr) {
			t.Fatal("probe accepted non-loopback", addr)
		}
	}
}
