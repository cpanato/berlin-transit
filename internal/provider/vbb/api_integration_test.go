package vbb

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

	"github.com/cpanato/berlin-transit/internal/httpapi"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/service"
)

// Exercise HTTP -> service -> VBB -> mock upstream, with no live network calls.
func TestAPICacheWithMockUpstream(t *testing.T) {
	for _, tc := range []struct{ path, body, field string }{
		{"/api/v1/stations?query=Alex", `[{"type":"stop","id":"900100003","name":"S+U Alexanderplatz"}]`, "stations"},
		{"/api/v1/stations/900100003/departures", `{"departures":[{"stop":{"id":"900100003","name":"S+U Alexanderplatz"},"line":{"name":"S7","product":"suburban"},"plannedWhen":"2026-10-18T18:35:00+02:00","when":"2026-10-18T18:38:00+02:00","delay":180}]}`, "departures"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			var calls atomic.Int64
			upstream := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Cache-Control", "max-age=30")
				writeResponse(t, w, tc.body)
			})
			s, err := service.New(context.Background(), upstream.Client, service.Config{StationsTTL: time.Hour, DeparturesTTL: 30 * time.Second, MaxStale: 5 * time.Minute, Timeout: time.Second, CacheEntries: 16, CacheBytes: 1 << 20, RequestsPerMinute: 30})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			server := httptest.NewUnstartedServer(nil)
			log := observability.Logger(io.Discard, slog.LevelInfo)
			server.Config.Handler = httpapi.New(s, log, false, server.Listener.Addr().String())
			server.Start()
			defer server.Close()
			for range 2 {
				resp, err := server.Client().Get(server.URL + tc.path)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				err = json.NewDecoder(resp.Body).Decode(&body)
				_ = resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || body[tc.field] == nil || body["source"] != "vbb" || body["stale"] != false {
					t.Fatalf("%d %+v %v", resp.StatusCode, body, err)
				}
			}
			if calls.Load() != 1 {
				t.Errorf("cache missed: %d upstream calls", calls.Load())
			}
		})
	}
}
