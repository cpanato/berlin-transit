package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/provider"
	"github.com/cpanato/berlin-transit/internal/service"
)

type batchCollector struct {
	fakeCollector
	mu      sync.Mutex
	errors  map[string]error
	options map[string]domain.Options
	panicID string
	staleID string
}

func (c *batchCollector) Departures(ctx context.Context, id string, o domain.Options) (domain.Departures, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.options == nil {
		c.options = make(map[string]domain.Options)
	}
	c.options[id] = o
	if id == c.panicID {
		panic("private raw body")
	}
	if err := ctx.Err(); err != nil {
		return domain.Departures{}, err
	}
	out := domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}, Source: "vbb"}
	if id == c.staleID {
		age := int64(31)
		last := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		out.Stale = true
		out.LastSuccessAt = &last
		out.StaleAgeSeconds = &age
	}
	return out, c.errors[id]
}

func batchAPI(c *batchCollector) *API {
	return New(c, observability.Logger(io.Discard, slog.LevelInfo), false, "127.0.0.1:8080")
}

func TestMultiValidation(t *testing.T) {
	a, c, _ := apiFixture()
	for _, q := range []string{"", "stations=", "stations=a,b,c,d,e,f", "stations=a,a", "stations=a,", "stations=,a", "stations=a,,b", "stations=a&stations=b", "stations=a,%2F", "stations=a,b&mode=train", "stations=a,b&limit=0", "stations=a,b&limit=51", "stations=a,b&duration=bad", "stations=a,b&duration=121", "stations=a,b&direction=%00", "stations=a,b&unknown=x", "stations=a," + strings.Repeat("x", 257), "stations=a,%zz", "stations=a,%20b"} {
		w := request(a, "/api/v1/departures?"+q)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
	}
	if c.calls != 0 {
		t.Fatal("invalid batch reached collector")
	}
}

func TestMultiResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		errs    map[string]error
		panicID string
		status  int
		partial bool
		retry   bool
	}{
		{name: "success", status: 200},
		{name: "partial", errs: map[string]error{"b": &provider.Error{Status: 404}}, status: 200, partial: true},
		{name: "uniform", errs: map[string]error{"a": &provider.Error{Status: 404}, "b": &provider.Error{Status: 404}}, status: 404},
		{name: "mixed", errs: map[string]error{"a": &provider.Error{Status: 404}, "b": errors.New("private raw body")}, status: 503},
		{name: "budget", errs: map[string]error{"a": &service.Error{Code: "rate_limited", RetryAfter: 1500 * time.Millisecond}, "b": &service.Error{Code: "rate_limited", RetryAfter: time.Second}}, status: 429, retry: true},
		{name: "panic", panicID: "b", status: 200, partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &batchCollector{errors: tc.errs, panicID: tc.panicID, staleID: "a"}
			w := request(batchAPI(c), "/api/v1/departures?stations=a,b&mode=bus&limit=5&duration=15&direction=dest:02")
			var out multiResponse
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || out.Partial != tc.partial || len(out.Results) != 2 || c.calls != 2 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			for i, id := range []string{"a", "b"} {
				r := out.Results[i]
				if r.StationID != id || c.options[id] != (domain.Options{Mode: "bus", Limit: 5, Duration: 15, Direction: "dest:02"}) {
					t.Fatal("order/options")
				}
				if (r.Data == nil) == (r.Error == nil) {
					t.Fatal("result must contain exactly data or error")
				}
				if r.Data != nil && id == "a" && (!r.Data.Stale || r.Data.Realtime || r.Data.LastSuccessAt == nil || *r.Data.StaleAgeSeconds != 31) {
					t.Fatal("lost freshness")
				}
			}
			if tc.retry && (w.Header().Get("Retry-After") != "2" || out.Results[0].RetryAfterSeconds != 2) {
				t.Fatal("retry metadata")
			}
			if strings.Contains(w.Body.String(), "private raw body") {
				t.Fatal("error leaked")
			}
		})
	}
}

func TestMultiDefaultsBoundsAndCancellation(t *testing.T) {
	c := &batchCollector{}
	a := batchAPI(c)
	w := request(a, "/api/v1/departures?stations=a,b,c,d,e")
	if w.Code != 200 || c.calls != 5 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, o := range c.options {
		if o != (domain.Options{Mode: "suburban", Limit: 10, Duration: 30}) {
			t.Fatal(o)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/v1/departures?stations=a,b", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	a.ServeHTTP(recorder, r)
	if recorder.Code != 503 || !strings.Contains(recorder.Body.String(), "request_cancelled") {
		t.Fatal(recorder.Body)
	}
}

type cancelCollector struct {
	fakeCollector
	started chan string
}

func (c *cancelCollector) Departures(ctx context.Context, id string, _ domain.Options) (domain.Departures, error) {
	c.started <- id
	<-ctx.Done()
	return domain.Departures{}, ctx.Err()
}

func TestMultiConcurrentCancellation(t *testing.T) {
	c := &cancelCollector{started: make(chan string, 5)}
	a := New(c, observability.Logger(io.Discard, slog.LevelInfo), false, "127.0.0.1:8080")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/v1/departures?stations=a,b,c,d,e", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(w, r) }()
	// Every independent read must start before any one of them completes.
	for range 5 {
		select {
		case <-c.started:
		case <-time.After(5 * time.Second):
			t.Fatal("station reads serialized or missing")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not finish all station reads")
	}
	var out multiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Code != 503 || len(out.Results) != 5 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, result := range out.Results {
		if result.Status != 503 || result.Error == nil || result.Error.Code != "request_cancelled" {
			t.Fatal(result)
		}
	}
}
