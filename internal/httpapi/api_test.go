package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/provider"
	"github.com/cpanato/berlin-transit/internal/service"
)

type fakeCollector struct {
	err       error
	panics    bool
	calls     int
	id, query string
	limit     int
	opts      domain.Options
}

func (c *fakeCollector) SearchStations(_ context.Context, q string, n int) (domain.Stations, error) {
	if c.panics {
		panic("secret upstream body")
	}
	c.calls++
	c.query = q
	c.limit = n
	return domain.Stations{Stations: []domain.Station{{ID: "id"}}, Source: "vbb"}, c.err
}
func (c *fakeCollector) Departures(_ context.Context, id string, o domain.Options) (domain.Departures, error) {
	c.calls++
	c.id = id
	c.opts = o
	return domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}, Source: "vbb"}, c.err
}

func apiFixture() (*API, *fakeCollector, *bytes.Buffer) {
	c := &fakeCollector{}
	log := &bytes.Buffer{}
	return New(c, observability.Logger(log, slog.LevelDebug), false, "127.0.0.1:8080"), c, log
}

func request(a *API, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+path, nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func TestStationAndDepartureRoutes(t *testing.T) {
	a, c, log := apiFixture()
	w := request(a, "/api/v1/stations?query=private+search&limit=3")
	if w.Code != 200 || c.query != "private search" || c.limit != 3 || !strings.Contains(w.Body.String(), `"stations":[`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for k, v := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Content-Type": "application/json; charset=utf-8"} {
		if w.Header().Get(k) != v {
			t.Error(k)
		}
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("missing request ID")
	}
	w = request(a, "/api/v1/stations/opaque:001/departures?mode=bus&direction=dest:02&limit=5&duration=15")
	if w.Code != 200 || c.id != "opaque:001" || c.opts.Mode != "bus" || c.opts.Limit != 5 || c.opts.Duration != 15 || c.opts.Direction != "dest:02" {
		t.Fatalf("%d %+v", w.Code, c.opts)
	}
	w = request(a, "/api/v1/stations/id/departures")
	if w.Code != 200 || c.opts.Mode != "suburban" || c.opts.Limit != 10 || c.opts.Duration != 30 {
		t.Fatal(c.opts)
	}
	w = request(a, "/api/v1/stations?query=Alex")
	if w.Code != 200 || c.limit != 10 {
		t.Fatal("search default")
	}
	for _, secret := range []string{"private search", "private+search", "opaque:001", "dest:02", "/api/v1/stations/id"} {
		if strings.Contains(log.String(), secret) {
			t.Fatalf("location data in logs: %s", log)
		}
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.Split(log.Bytes(), []byte("\n"))[0], &event); err != nil || event["request_id"] == nil || event["duration_ms"] == nil {
		t.Fatalf("invalid structured log: %v", err)
	}
}

func TestValidationBeforeProvider(t *testing.T) {
	a, c, _ := apiFixture()
	for _, path := range []string{
		"/api/v1/stations", "/api/v1/stations?query=", "/api/v1/stations?query=x&limit=0", "/api/v1/stations?query=x&limit=51", "/api/v1/stations?query=x&limit=bad", "/api/v1/stations?query=x&query=y", "/api/v1/stations?query=%zz", "/api/v1/stations?query=x&unknown=y", "/api/v1/stations?query=" + strings.Repeat("x", 201), "/api/v1/stations?query=%00",
		"/api/v1/stations/../departures", "/api/v1/stations/a%2Fb/departures", "/api/v1/stations/a%252fb/departures", "/api/v1/stations/id/departures?mode=train", "/api/v1/stations/id/departures?limit=0", "/api/v1/stations/id/departures?duration=0", "/api/v1/stations/id/departures?duration=121", "/api/v1/stations/id/departures?duration=bad", "/api/v1/stations/id/departures?mode=bus&mode=tram", "/api/v1/stations/id/departures?direction=a%2Fb", "/api/v1/stations/id/departures?unknown=x",
	} {
		t.Run(path, func(t *testing.T) {
			w := request(a, path)
			if w.Code != 400 {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	if c.calls != 0 {
		t.Fatalf("made %d invalid provider calls", c.calls)
	}
}

func TestOperationalEndpoints(t *testing.T) {
	a, c, _ := apiFixture()
	c.err = &provider.Error{Status: 503}
	for _, path := range []string{"/healthz", "/readyz"} {
		if w := request(a, path); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if c.calls != 0 {
		t.Error("health depends on upstream")
	}
	if w := request(a, "/metrics"); w.Code != 404 {
		t.Error("metrics enabled by default")
	}
	if w := request(a, "/api/v1/departures?stations=id"); w.Code != 404 {
		t.Error("premature multi-station route")
	}
	if w := request(a, "/debug/pprof/"); w.Code != 404 {
		t.Error("debug listener")
	}
	a.NotReady()
	if w := request(a, "/readyz"); w.Code != 503 || !strings.Contains(w.Body.String(), "not_ready") {
		t.Fatal(w.Code)
	}
	if w := request(a, "/healthz"); w.Code != 200 {
		t.Fatal("shutdown changed liveness")
	}
	a, _, _ = apiFixture()
	a.metricsEnabled = true
	request(a, "/healthz")
	w := request(a, "/metrics")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/plain") || !strings.Contains(w.Body.String(), "requests_total 1") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestRequestSecurity(t *testing.T) {
	a, c, log := apiFixture()
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"host", func(r *http.Request) { r.Host = "attacker.example" }, 403},
		{"wrong port", func(r *http.Request) { r.Host = "127.0.0.1:9090" }, 403},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"method", func(r *http.Request) { r.Method = "POST" }, 405},
		{"body", func(r *http.Request) { r.ContentLength = 1 }, 400},
		{"chunked body", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, 400},
		{"query length", func(r *http.Request) { r.URL.RawQuery = strings.Repeat("x", 2049) }, 400},
		{"path length", func(r *http.Request) { r.URL.Path = "/" + strings.Repeat("x", 1024) }, 400},
		{"request length", func(r *http.Request) { r.RequestURI = strings.Repeat("x", 3073) }, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/v1/stations?query=secret", nil)
			r.Header.Set("Authorization", "Bearer private-token")
			r.Header.Set("X-Request-ID", "untrusted-input")
			tc.change(r)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if w.Header().Get("X-Request-ID") == "untrusted-input" {
				t.Fatal("accepted caller ID")
			}
		})
	}
	if c.calls != 0 {
		t.Error("unsafe request reached provider")
	}
	for _, s := range []string{"secret", "private-token", "attacker.example", "untrusted-input"} {
		if strings.Contains(log.String(), s) {
			t.Fatal("sensitive input logged")
		}
	}
	r := httptest.NewRequest("GET", "http://localhost:8080/healthz", nil)
	r.Header.Set("Origin", "http://localhost:8080")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("same-origin localhost rejected")
	}
}

func TestSanitizedErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrInput, 400, "invalid_request"}, {context.Canceled, 503, "request_cancelled"}, {context.DeadlineExceeded, 503, "upstream_timeout"},
		{&service.Error{Code: "rate_limited", RetryAfter: 1500 * time.Millisecond}, 429, "rate_limited"}, {&service.Error{Code: "overloaded"}, 503, "overloaded"}, {&service.Error{Code: "unavailable"}, 503, "unavailable"},
		{&provider.Error{Status: 404}, 404, "station_not_found"}, {&provider.Error{Status: 429, RetryAt: time.Now().Add(time.Minute)}, 429, "upstream_rate_limited"},
		{&provider.Error{Status: 503}, 503, "upstream_unavailable"}, {&provider.Error{Code: "unavailable"}, 503, "upstream_unavailable"}, {&provider.Error{Code: "private raw body"}, 502, "bad_gateway"}, {errors.New("private raw body"), 502, "bad_gateway"},
	} {
		t.Run(tc.code+tc.err.Error(), func(t *testing.T) {
			a, c, log := apiFixture()
			c.err = tc.err
			for _, path := range []string{"/api/v1/stations?query=secret", "/api/v1/stations/id/departures"} {
				w := request(a, path)
				if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				if tc.status == 429 && w.Header().Get("Retry-After") == "" {
					t.Fatal("missing retry deadline")
				}
				if strings.Contains(w.Body.String(), "raw body") || strings.Contains(log.String(), "raw body") {
					t.Fatal("upstream error leaked")
				}
			}
		})
	}
}

func TestOverloadAndRecovery(t *testing.T) {
	a, c, log := apiFixture()
	for range cap(a.active) {
		a.active <- struct{}{}
	}
	w := request(a, "/api/v1/stations?query=x")
	if w.Code != 503 || c.calls != 0 {
		t.Fatal("unbounded requests")
	}
	if w := request(a, "/healthz"); w.Code != 200 {
		t.Fatal("overload broke health")
	}
	for range cap(a.active) {
		<-a.active
	}
	c.panics = true
	w = request(a, "/api/v1/stations?query=x")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "internal_error") || strings.Contains(log.String(), "secret upstream body") {
		t.Fatalf("panic exposed: %d %s", w.Code, log)
	}
}

func TestServerTimeoutsAndJSONFailure(t *testing.T) {
	log := observability.Logger(io.Discard, slog.LevelInfo)
	server := Server("127.0.0.1:8080", nil, 10*time.Second, log)
	if server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 10*time.Second || server.WriteTimeout != 15*time.Second || server.IdleTimeout != time.Minute || server.MaxHeaderBytes != 16<<10 || server.ErrorLog == nil {
		t.Fatal("missing HTTP hard limits")
	}
	w := &response{ResponseWriter: httptest.NewRecorder()}
	writeJSON(w, 200, make(chan int))
	if w.status != 500 {
		t.Fatal("marshal error ignored")
	}
	w = &response{ResponseWriter: httptest.NewRecorder()}
	if _, err := w.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	w.WriteHeader(500)
	if w.status != 200 {
		t.Fatal("duplicate status")
	}
	for _, d := range []time.Duration{-1, 2 * time.Second, 25 * time.Hour} {
		w := &response{ResponseWriter: httptest.NewRecorder()}
		retry(w, d)
		if w.Header().Get("Retry-After") == "" {
			t.Fatal("missing retry header")
		}
	}
}

func FuzzQuery(f *testing.F) {
	for _, s := range []string{"query=Alexanderplatz&limit=10", "mode=suburban&duration=30", "limit=0", "query=%zz", "query=a&query=b", "unknown=x", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q, err := query(s, "query", "limit", "mode", "duration", "direction")
		if err != nil {
			return
		}
		for _, values := range q {
			if len(values) != 1 || values[0] == "" {
				t.Fatal("ambiguous query accepted")
			}
		}
		_, _ = number(q, "limit", 10, 50)
		_, _ = number(q, "duration", 30, 120)
	})
}
