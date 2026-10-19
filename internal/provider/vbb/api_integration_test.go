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

	"github.com/cpanato/berlin-transit/internal/domain"
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

// A cached station remains available when another station exhausts the shared
// budget. Batch reads reuse the exact same keys as single-station requests.
func TestMultiStationCacheAndBudget(t *testing.T) {
	var calls atomic.Int64
	upstream := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/stops/cached/departures" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		w.Header().Set("Cache-Control", "max-age=30")
		writeResponse(t, w, `{"departures":[]}`)
	})
	s, err := service.New(context.Background(), upstream.Client, service.Config{StationsTTL: time.Hour, DeparturesTTL: 30 * time.Second, MaxStale: 5 * time.Minute, Timeout: time.Second, CacheEntries: 16, CacheBytes: 1 << 20, RequestsPerMinute: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := httpapi.New(s, observability.Logger(io.Discard, slog.LevelInfo), false, "127.0.0.1:8080")
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil))
		return w
	}
	if w := get("/api/v1/stations/cached/departures"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	for range 2 {
		w := get("/api/v1/departures?stations=cached,uncached")
		var out struct {
			Partial bool `json:"partial"`
			Results []struct {
				Status     int                `json:"status"`
				Data       *domain.Departures `json:"data"`
				RetryAfter int                `json:"retry_after_seconds"`
			} `json:"results"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || !out.Partial || len(out.Results) != 2 || out.Results[0].Status != 200 || out.Results[0].Data == nil || out.Results[0].Data.Station.ID != "cached" || out.Results[1].Status != 429 || out.Results[1].RetryAfter < 1 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if w := get("/api/v1/departures?stations=cached"); w.Code != 200 {
		t.Fatal(w.Body)
	}
	// Options that affect results must not accidentally reuse the warm entry.
	if w := get("/api/v1/departures?stations=cached&limit=1"); w.Code != 429 {
		t.Fatalf("options lost: %d %s", w.Code, w.Body)
	}
	if calls.Load() != 1 {
		t.Fatalf("batch bypassed budget: %d", calls.Load())
	}
}
