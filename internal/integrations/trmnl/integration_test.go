package trmnl

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/collector"
	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/provider"
	"github.com/cpanato/berlin-transit/internal/service"
)

type fixtureProvider struct{ calls atomic.Int64 }

func (p *fixtureProvider) SearchStations(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
	return provider.Result[[]domain.Station]{}, nil
}
func (p *fixtureProvider) Departures(_ context.Context, id string, _ domain.Options, _ string) (provider.Result[domain.Departures], error) {
	p.calls.Add(1)
	return provider.Result[domain.Departures]{Data: domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}, Source: "vbb"}}, nil
}

// Exercise actual opt-in worker entrypoints: collection -> service/cache ->
// cache-only publisher -> mock webhook. No live VBB or TRMNL calls occur.
func TestCollectionToMockWebhook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fixtureProvider{}
	s, err := service.New(ctx, p, service.Config{StationsTTL: time.Hour, DeparturesTTL: 30 * time.Second, MaxStale: 5 * time.Minute, Timeout: time.Second, CacheEntries: 8, CacheBytes: 1 << 20, RequestsPerMinute: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := domain.Options{Mode: "suburban", Limit: 8, Duration: 30}
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		collector.Run(ctx, collector.Config{Enabled: true, Stations: []string{"id"}, Options: opts, Interval: 30 * time.Second}, s, log)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := s.CachedDepartures("id", opts); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("collector did not warm cache")
		}
		time.Sleep(time.Millisecond)
	}
	received := make(chan payload, 1)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p payload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		received <- p
		w.WriteHeader(200)
	}))
	defer mock.Close()
	cfg := goodConfig()
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.url = mock.URL
	c.http.Transport = &http.Transport{}
	published := make(chan struct{})
	go func() { defer close(published); Run(ctx, cfg, c, s, []string{"id"}, opts, log) }()
	select {
	case got := <-received:
		if len(got.Variables.Stations) != 1 || got.Variables.Stations[0].Station.ID != "id" || got.Variables.Stations[0].CollectedAt.IsZero() || got.Variables.Stations[0].Stale {
			t.Fatal("incorrect published snapshot")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook did not receive cached data")
	}
	cancel()
	for _, done := range []chan struct{}{collected, published} {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("worker shutdown hung")
		}
	}
	if p.calls.Load() != 1 {
		t.Fatalf("publisher fetched upstream: %d", p.calls.Load())
	}
}
