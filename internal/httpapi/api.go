// Package httpapi exposes the versioned JSON API on the private listener.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/provider"
	"github.com/cpanato/berlin-transit/internal/service"
)

type Collector interface {
	SearchStations(context.Context, string, int) (domain.Stations, error)
	Departures(context.Context, string, domain.Options) (domain.Departures, error)
}

type API struct {
	collector      Collector
	log            *slog.Logger
	metrics        observability.Metrics
	metricsEnabled bool
	ready          atomic.Bool
	sequence       atomic.Uint64
	hosts          map[string]bool
	active         chan struct{}
}

func New(c Collector, log *slog.Logger, metrics bool, addr string) *API {
	_, port, _ := net.SplitHostPort(addr)
	a := &API{collector: c, log: log, metricsEnabled: metrics, hosts: map[string]bool{addr: true, net.JoinHostPort("localhost", port): true}, active: make(chan struct{}, 128)}
	a.ready.Store(true)
	return a
}

func (a *API) NotReady() { a.ready.Store(false) }

func Server(addr string, h http.Handler, timeout time.Duration, log *slog.Logger) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: timeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError)}
}

type response struct {
	http.ResponseWriter
	status int
	code   string
}

func (w *response) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *response) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func writeJSON(w *response, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fail(w, 500, "internal_error")
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func fail(w *response, status int, code string) {
	w.code = code
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": http.StatusText(status)}})
}

func (a *API) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := strconv.FormatUint(a.sequence.Add(1), 16)
	w := &response{ResponseWriter: rw}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", id)
	route := "unknown"
	defer func() {
		if recover() != nil {
			fail(w, 500, "internal_error")
		}
		elapsed := time.Since(start)
		a.metrics.Observe(w.status, elapsed)
		level := slog.LevelInfo
		if w.status >= 500 {
			level = slog.LevelWarn
		}
		a.log.Log(r.Context(), level, "request", "request_id", id, "route", route, "status", w.status, "duration_ms", elapsed.Milliseconds(), "error_code", w.code)
	}()
	// Exact loopback Host validation prevents DNS rebinding; no CORS or cookie
	// credentials are enabled. Cross-origin browser calls are rejected.
	if !a.hosts[r.Host] || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "forbidden")
		return
	}
	if len(r.RequestURI) > 3072 || len(r.URL.RawQuery) > 2048 || len(r.URL.Path) > 1024 {
		fail(w, 400, "invalid_request")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, 405, "method_not_allowed")
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		fail(w, 400, "invalid_request")
		return
	}
	switch r.URL.Path {
	case "/healthz":
		route = "health"
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	case "/readyz":
		route = "ready"
		if !a.ready.Load() {
			fail(w, 503, "not_ready")
		} else {
			writeJSON(w, 200, map[string]string{"status": "ready"})
		}
		return
	case "/metrics":
		if !a.metricsEnabled {
			fail(w, 404, "not_found")
			return
		}
		route = "metrics"
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(200)
		_ = a.metrics.Write(w)
		return
	}
	select {
	case a.active <- struct{}{}:
		defer func() { <-a.active }()
	default:
		retry(w, time.Second)
		fail(w, 503, "overloaded")
		return
	}
	if r.URL.Path == "/api/v1/stations" {
		route = "stations"
		q, err := query(r.URL.RawQuery, "query", "limit")
		if err != nil {
			fail(w, 400, "invalid_request")
			return
		}
		limit, err := number(q, "limit", 10, 50)
		if err != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if _, err := domain.SearchQuery(q.Get("query"), limit); err != nil {
			fail(w, 400, "invalid_request")
			return
		}
		out, err := a.collector.SearchStations(r.Context(), q.Get("query"), limit)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/stations/") && strings.HasSuffix(r.URL.Path, "/departures") {
		route = "departures"
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/stations/"), "/departures")
		q, err := query(r.URL.RawQuery, "mode", "limit", "duration", "direction")
		if err != nil {
			fail(w, 400, "invalid_request")
			return
		}
		limit, e1 := number(q, "limit", 10, 50)
		duration, e2 := number(q, "duration", 30, 120)
		o, err := domain.NormalizeOptions(id, domain.Options{Mode: q.Get("mode"), Limit: limit, Duration: duration, Direction: q.Get("direction")})
		if err != nil || e1 != nil || e2 != nil {
			fail(w, 400, "invalid_request")
			return
		}
		out, err := a.collector.Departures(r.Context(), id, o)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, out)
		return
	}
	fail(w, 404, "not_found")
}

func query(raw string, allowed ...string) (url.Values, error) {
	if len(raw) > 2048 {
		return nil, domain.ErrInput
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return nil, domain.ErrInput
	}
	for name, values := range q {
		found := false
		for _, a := range allowed {
			found = found || name == a
		}
		if !found || len(values) != 1 || values[0] == "" {
			return nil, domain.ErrInput
		}
	}
	return q, nil
}

func number(q url.Values, name string, def, max int) (int, error) {
	if _, ok := q[name]; !ok {
		return def, nil
	}
	n, err := strconv.Atoi(q.Get(name))
	if err != nil || n < 1 || n > max {
		return 0, domain.ErrInput
	}
	return n, nil
}

func retry(w *response, d time.Duration) {
	n := int64(math.Ceil(d.Seconds()))
	if n < 1 {
		n = 1
	}
	if n > 86400 {
		n = 86400
	}
	w.Header().Set("Retry-After", strconv.FormatInt(n, 10))
}

func (a *API) failure(w *response, err error) {
	var local *service.Error
	var upstream *provider.Error
	switch {
	case errors.Is(err, domain.ErrInput):
		fail(w, 400, "invalid_request")
	case errors.Is(err, context.Canceled):
		fail(w, 503, "request_cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, 503, "upstream_timeout")
	case errors.As(err, &local):
		switch local.Code {
		case "rate_limited":
			retry(w, local.RetryAfter)
			fail(w, 429, "rate_limited")
		case "overloaded":
			retry(w, time.Second)
			fail(w, 503, "overloaded")
		default:
			retry(w, time.Second)
			fail(w, 503, "unavailable")
		}
	case errors.As(err, &upstream):
		switch upstream.Status {
		case 404:
			fail(w, 404, "station_not_found")
		case 429:
			retry(w, time.Until(upstream.RetryAt))
			fail(w, 429, "upstream_rate_limited")
		case 503:
			fail(w, 503, "upstream_unavailable")
		default:
			if upstream.Code == "unavailable" {
				fail(w, 503, "upstream_unavailable")
			} else {
				fail(w, 502, "bad_gateway")
			}
		}
	default:
		fail(w, 502, "bad_gateway")
	}
}
