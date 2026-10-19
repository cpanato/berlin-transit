package config

import (
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

func (c *Config) loadBackground(getenv func(string) string) error {
	c.Collector.Interval = 30 * time.Second
	c.Collector.Options = domain.Options{Mode: "suburban", Limit: 8, Duration: 30}
	c.TRMNL.Interval = 5 * time.Minute
	c.TRMNL.MaxDepartures = 8
	for _, v := range []struct {
		name   string
		target *bool
	}{
		{"BERLIN_TRANSIT_COLLECTOR_ENABLED", &c.Collector.Enabled}, {"TRMNL_ENABLED", &c.TRMNL.Enabled},
	} {
		if raw := getenv(v.name); raw != "" {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return invalid(v.name)
			}
			*v.target = b
		}
	}
	for _, v := range []struct {
		name     string
		target   *time.Duration
		min, max time.Duration
	}{
		{"BERLIN_TRANSIT_COLLECTOR_INTERVAL", &c.Collector.Interval, 30 * time.Second, time.Hour},
		{"TRMNL_INTERVAL", &c.TRMNL.Interval, 5 * time.Minute, 24 * time.Hour},
	} {
		if raw := getenv(v.name); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil || d < v.min || d > v.max {
				return invalid(v.name)
			}
			*v.target = d
		}
	}
	for _, v := range []struct {
		name   string
		target *int
		max    int
	}{
		{"BERLIN_TRANSIT_COLLECTOR_LIMIT", &c.Collector.Options.Limit, 50},
		{"BERLIN_TRANSIT_COLLECTOR_DURATION", &c.Collector.Options.Duration, 120},
		{"TRMNL_MAX_DEPARTURES", &c.TRMNL.MaxDepartures, 8},
	} {
		if raw := getenv(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > v.max {
				return invalid(v.name)
			}
			*v.target = n
		}
	}
	if raw := getenv("BERLIN_TRANSIT_COLLECTOR_STATIONS"); raw != "" {
		if len(raw) > 1284 {
			return invalid("BERLIN_TRANSIT_COLLECTOR_STATIONS")
		}
		c.Collector.Stations = strings.Split(raw, ",")
		if len(c.Collector.Stations) > 5 {
			return invalid("BERLIN_TRANSIT_COLLECTOR_STATIONS")
		}
		seen := map[string]bool{}
		for _, id := range c.Collector.Stations {
			if !domain.ValidID(id) || seen[id] {
				return invalid("BERLIN_TRANSIT_COLLECTOR_STATIONS")
			}
			seen[id] = true
		}
	}
	if raw := getenv("BERLIN_TRANSIT_COLLECTOR_MODE"); raw != "" {
		c.Collector.Options.Mode = raw
	}
	c.Collector.Options.Direction = getenv("BERLIN_TRANSIT_COLLECTOR_DIRECTION")
	if _, err := domain.NormalizeOptions("validation", c.Collector.Options); err != nil {
		return invalid("BERLIN_TRANSIT_COLLECTOR_OPTIONS")
	}
	if c.Collector.Validate(c.Service.RequestsPerMinute) != nil {
		return invalid("BERLIN_TRANSIT_COLLECTOR_CONFIGURATION")
	}
	c.TRMNL.WebhookURL = getenv("TRMNL_WEBHOOK_URL")
	if c.TRMNL.Enabled && !c.Collector.Enabled {
		return invalid("TRMNL_ENABLED")
	}
	if c.TRMNL.Validate() != nil {
		return invalid("TRMNL_CONFIGURATION")
	}
	return nil
}
