// Package config validates environment configuration before opening a listener.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/cpanato/berlin-transit/internal/provider/vbb"
	"github.com/cpanato/berlin-transit/internal/service"
)

type Config struct {
	ListenAddr, BaseURL string
	HTTPTimeout         time.Duration
	MetricsEnabled      bool
	LogLevel            slog.Level
	Service             service.Config
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{ListenAddr: "127.0.0.1:8080", BaseURL: vbb.Origin, HTTPTimeout: 10 * time.Second, LogLevel: slog.LevelInfo}
	c.Service = service.Config{StationsTTL: 24 * time.Hour, DeparturesTTL: 30 * time.Second, MaxStale: 5 * time.Minute, CacheEntries: 256, CacheBytes: 16 << 20, RequestsPerMinute: 30}
	for name, dest := range map[string]*string{"BERLIN_TRANSIT_LISTEN_ADDR": &c.ListenAddr, "BERLIN_TRANSIT_VBB_BASE_URL": &c.BaseURL} {
		if v := getenv(name); v != "" {
			*dest = v
		}
	}
	if !LoopbackAddr(c.ListenAddr) {
		return Config{}, invalid("BERLIN_TRANSIT_LISTEN_ADDR")
	}
	if c.BaseURL != vbb.Origin && c.BaseURL != vbb.Origin+"/" {
		return Config{}, invalid("BERLIN_TRANSIT_VBB_BASE_URL")
	}
	for _, v := range []struct {
		name     string
		target   *time.Duration
		min, max time.Duration
	}{
		{"BERLIN_TRANSIT_HTTP_TIMEOUT", &c.HTTPTimeout, time.Millisecond, time.Minute},
		{"BERLIN_TRANSIT_DEPARTURES_TTL", &c.Service.DeparturesTTL, time.Millisecond, 5 * time.Minute},
		{"BERLIN_TRANSIT_STATIONS_TTL", &c.Service.StationsTTL, time.Millisecond, 7 * 24 * time.Hour},
		{"BERLIN_TRANSIT_MAX_STALE", &c.Service.MaxStale, 0, 24 * time.Hour},
	} {
		if raw := getenv(v.name); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil || d < v.min || d > v.max {
				return Config{}, invalid(v.name)
			}
			*v.target = d
		}
	}
	for _, v := range []struct {
		name     string
		target   *int
		min, max int
	}{
		{"BERLIN_TRANSIT_CACHE_ENTRIES", &c.Service.CacheEntries, 1, 4096},
		{"BERLIN_TRANSIT_CACHE_BYTES", &c.Service.CacheBytes, 1 << 20, 64 << 20},
		{"BERLIN_TRANSIT_REQUESTS_PER_MINUTE", &c.Service.RequestsPerMinute, 1, 60},
	} {
		if raw := getenv(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < v.min || n > v.max {
				return Config{}, invalid(v.name)
			}
			*v.target = n
		}
	}
	if raw := getenv("BERLIN_TRANSIT_METRICS_ENABLED"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, invalid("BERLIN_TRANSIT_METRICS_ENABLED")
		}
		c.MetricsEnabled = b
	}
	if raw := getenv("BERLIN_TRANSIT_LOG_LEVEL"); raw != "" {
		if err := c.LogLevel.UnmarshalText([]byte(raw)); err != nil {
			return Config{}, invalid("BERLIN_TRANSIT_LOG_LEVEL")
		}
	}
	c.Service.Timeout = c.HTTPTimeout
	return c, nil
}

func invalid(name string) error { return fmt.Errorf("invalid %s", name) }

// M2 deliberately supports only literal loopback addresses. Remote/private LAN
// opt-in and deployment controls belong to M4, not a wildcard listener flag.
func LoopbackAddr(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535
}
