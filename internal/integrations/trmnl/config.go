// Package trmnl provides an optional, cache-only private-plugin output adapter.
package trmnl

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Enabled       bool
	WebhookURL    string
	Interval      time.Duration
	MaxDepartures int
}

// Avoid accidental disclosure when configuration is formatted for diagnostics.
func (Config) String() string { return "TRMNL configuration (redacted)" }

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !validWebhook(c.WebhookURL) || c.Interval < 5*time.Minute || c.Interval > 24*time.Hour || c.MaxDepartures < 1 || c.MaxDepartures > 8 {
		return errors.New("invalid TRMNL configuration")
	}
	return nil
}

func validWebhook(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "trmnl.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	token, ok := strings.CutPrefix(u.Path, "/api/custom_plugins/")
	if !ok || len(token) < 1 || len(token) > 128 {
		return false
	}
	for _, c := range token {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
