package trmnl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

func goodConfig() Config {
	return Config{Enabled: true, WebhookURL: "https://trmnl.com/api/custom_plugins/test-token", Interval: 5 * time.Minute, MaxDepartures: 8}
}
func TestConfigurationAndTransport(t *testing.T) {
	cfg := goodConfig()
	if cfg.Validate() != nil || (Config{}).Validate() != nil {
		t.Fatal("valid defaults")
	}
	for _, raw := range []string{"http://trmnl.com/api/custom_plugins/secret", "https://trmnl.com.evil/api/custom_plugins/secret", "https://user:secret@trmnl.com/api/custom_plugins/x", "https://trmnl.com:443/api/custom_plugins/x", "https://127.0.0.1/api/custom_plugins/x", "https://trmnl.com/api/custom_plugins/", "https://trmnl.com/api/custom_plugins/x/y", "https://trmnl.com/api/custom_plugins/x?secret=x", "https://trmnl.com/api/custom_plugins/x?", "https://trmnl.com/api/custom_plugins/x#secret", "https://trmnl.com/api/custom_plugins/%78", "https://trmnl.com/api/custom_plugins/" + strings.Repeat("x", 129), "https://trmnl.com/api/custom_plugins/中文", "https://%zz"} {
		c := cfg
		c.WebhookURL = raw
		if c.Validate() == nil {
			t.Fatal("unsafe webhook", raw)
		}
	}
	for _, change := range []func(*Config){func(c *Config) { c.Interval = time.Minute }, func(c *Config) { c.Interval = 25 * time.Hour }, func(c *Config) { c.MaxDepartures = 0 }, func(c *Config) { c.MaxDepartures = 9 }} {
		c := cfg
		change(&c)
		if c.Validate() == nil {
			t.Fatal("invalid bounds")
		}
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tr := c.http.Transport.(*http.Transport)
	if tr.Proxy != nil || tr.MaxConnsPerHost != 1 || c.http.Timeout != 10*time.Second || tr.MaxResponseHeaderBytes != 16<<10 {
		t.Fatal("unsafe transport")
	}
	if c.http.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("redirect followed")
	}
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("unsafe dial")
	}
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("disabled client")
	}
	bad := cfg
	bad.WebhookURL = "secret"
	if _, err := NewClient(bad); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", cfg), "test-token") {
		t.Fatal("config leaks URL")
	}
}

type cached map[string]domain.Departures

func (c cached) CachedDepartures(id string, _ domain.Options) (domain.Departures, bool) {
	d, ok := c[id]
	return d, ok
}
func sample() cached {
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	line, dest, platform := "S7", "Potsdam Hbf", "2"
	delay := 0
	return cached{"a": {Station: domain.Station{ID: "a", Name: &dest}, Source: "vbb", FetchedAt: now, Realtime: true, Departures: []domain.Departure{{Line: &line, Direction: &dest, PlannedAt: &now, PredictedAt: &now, DelaySeconds: &delay, Platform: &platform}, {Cancelled: true}}}}
}

func TestPayloadFreshnessAndBounds(t *testing.T) {
	data := sample()
	now := data["a"].FetchedAt
	b, hash, err := build(data, []string{"a", "missing"}, domain.Options{}, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	g := p.Variables.Stations[0]
	if len(p.Variables.UnavailableStations) != 1 || p.Variables.UnavailableStations[0] != "missing" || len(g.Departures) != 1 || *g.Departures[0].DelaySeconds != 0 || !g.Realtime || g.Stale || !g.CollectedAt.Equal(now) || !p.Variables.GeneratedAt.Equal(now) {
		t.Fatalf("%s", b)
	}
	_, same, err := build(data, []string{"a", "missing"}, domain.Options{}, 1, now.Add(time.Minute))
	if err != nil || hash != same {
		t.Fatal("generation time breaks deduplication")
	}
	d := data["a"]
	d.Stale = true
	d.LastSuccessAt = &now
	age := int64(31)
	d.StaleAgeSeconds = &age
	data["a"] = d
	b, changed, err := build(data, []string{"a"}, domain.Options{}, 8, now)
	if err != nil || changed == hash {
		t.Fatal("freshness change lost")
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Variables.Stations[0].Realtime || !p.Variables.Stations[0].Stale || p.Variables.Stations[0].LastSuccessAt == nil || *p.Variables.Stations[0].StaleAgeSeconds != 31 {
		t.Fatal("stale realtime")
	}
	huge := strings.Repeat("<", 2000)
	d.Station.Name = &huge
	data["a"] = d
	if _, _, err := build(data, []string{"a"}, domain.Options{}, 8, now); err == nil || err.Error() != "payload_too_large" {
		t.Fatal(err)
	}
	d.Station.Name = nil
	d.FetchedAt = time.Time{}
	data["a"] = d
	if _, _, err := build(data, []string{"a"}, domain.Options{}, 8, now); err == nil || err.Error() != "no_snapshot" {
		t.Fatal(err)
	}
	d.FetchedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	data["a"] = d
	if _, _, err := build(data, []string{"a"}, domain.Options{}, 8, now); err == nil || err.Error() != "invalid_snapshot" {
		t.Fatal(err)
	}
}

func TestClientMockResponses(t *testing.T) {
	for _, status := range []int{200, 204, 301, 403, 422, 429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid request")
				}
				b, err := io.ReadAll(r.Body)
				if err != nil || string(b) != `{"merge_variables":{}}` {
					t.Error("invalid payload")
				}
				w.Header().Set("Retry-After", "60")
				w.Header().Set("Location", "http://127.0.0.1/secret")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(strings.Repeat("secret", 2000)))
			}))
			defer server.Close()
			cfg := goodConfig()
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.http.Transport = &http.Transport{}  // private mock transport only
			c.url = server.URL + "/private-token" // private test seam; no production override
			now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
			c.now = func() time.Time { return now }
			err = c.send(context.Background(), []byte(`{"merge_variables":{}}`))
			if status < 300 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var e *sendError
				if !errors.As(err, &e) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-token") {
					t.Fatal(err)
				}
				if e.temporary != (status == 429 || status >= 500) {
					t.Fatal(e)
				}
				if e.temporary && !e.retryAt.Equal(now.Add(time.Minute)) {
					t.Fatal("retry-after")
				}
			}
			if calls != 1 {
				t.Fatal("automatic retries or redirect")
			}
			if err := c.send(context.Background(), make([]byte, maxPayload+1)); err == nil {
				t.Fatal("oversized request")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := c.send(ctx, []byte("{}")); err == nil {
				t.Fatal("cancelled request sent")
			}
			if calls != 1 {
				t.Fatal("bounds/cancellation called server")
			}
			c.url = ":"
			if err := c.send(context.Background(), nil); err == nil {
				t.Fatal("bad internal URL")
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw  string
		want time.Time
	}{
		{"60", now.Add(time.Minute)}, {"99999999999", now.Add(24 * time.Hour)}, {now.Add(time.Minute).Format(http.TimeFormat), now.Add(time.Minute)}, {now.Add(48 * time.Hour).Format(http.TimeFormat), now.Add(24 * time.Hour)}, {"-1", time.Time{}}, {"secret", time.Time{}}, {strings.Repeat("x", 129), time.Time{}}, {now.Add(-time.Hour).Format(http.TimeFormat), time.Time{}},
	} {
		if got := retryAt(tc.raw, now); !got.Equal(tc.want) {
			t.Errorf("%q: %v", tc.raw, got)
		}
	}
}

func TestWorkerDeduplicationAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     error
		maxCalls int
	}{
		{"success", nil, 1}, {"transient", &sendError{code: "unavailable", temporary: true}, 3}, {"permanent", &sendError{code: "rejected"}, 1}, {"unknown", errors.New("private-token secret"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
			calls, ticks := 0, 0
			var logs bytes.Buffer
			send := func(context.Context, []byte) error { calls++; return tc.fail }
			sleep := func(_ context.Context, d time.Duration) error {
				if d < 5*time.Minute {
					t.Fatal("budget exceeded")
				}
				now = now.Add(d)
				ticks++
				if ticks == 8 {
					cancel()
				}
				return nil
			}
			run(ctx, goodConfig(), send, sample(), []string{"a"}, domain.Options{}, slog.New(slog.NewJSONHandler(&logs, nil)), func() time.Time { return now }, sleep, func() time.Duration { return 0 })
			if calls != tc.maxCalls {
				t.Fatalf("calls=%d", calls)
			}
			for _, secret := range []string{"private-token", "secret", "Potsdam", "test-token"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("log leak")
				}
			}
		})
	}
	// Disabled has no client, source or logger and must still be inert.
	Run(context.Background(), Config{}, nil, nil, nil, domain.Options{}, nil)
}

func TestWorkerRetryAfterAndMissingData(t *testing.T) {
	now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	start := now
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, ticks := 0, 0
	data := sample()
	send := func(context.Context, []byte) error {
		calls++
		if calls == 1 {
			return &sendError{code: "unavailable", temporary: true, retryAt: start.Add(20 * time.Minute)}
		}
		if now.Before(start.Add(20 * time.Minute)) {
			t.Fatal("ignored Retry-After")
		}
		return nil
	}
	sleep := func(_ context.Context, d time.Duration) error {
		ticks++
		now = now.Add(d)
		if ticks == 6 {
			return context.Canceled
		}
		return nil
	}
	run(ctx, goodConfig(), send, data, []string{"a"}, domain.Options{}, slog.Default(), func() time.Time { return now }, sleep, func() time.Duration { return 0 })
	if calls != 2 {
		t.Fatal(calls)
	}
	run(ctx, goodConfig(), func(context.Context, []byte) error { t.Fatal("no history sent"); return nil }, cached{}, []string{"a"}, domain.Options{}, slog.Default(), func() time.Time { return now }, func(context.Context, time.Duration) error { return context.Canceled }, func() time.Duration { return 0 })
}
