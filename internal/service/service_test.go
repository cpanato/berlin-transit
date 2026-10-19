package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/provider"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fake struct {
	search func(context.Context, string, int, string) (provider.Result[[]domain.Station], error)
	depart func(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error)
	calls  atomic.Int64
}

func (f *fake) SearchStations(ctx context.Context, q string, n int, tag string) (provider.Result[[]domain.Station], error) {
	f.calls.Add(1)
	if f.search != nil {
		return f.search(ctx, q, n, tag)
	}
	name := "Station"
	return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "opaque:001", Name: &name}}}, nil
}
func (f *fake) Departures(ctx context.Context, id string, o domain.Options, tag string) (provider.Result[domain.Departures], error) {
	f.calls.Add(1)
	if f.depart != nil {
		return f.depart(ctx, id, o, tag)
	}
	return provider.Result[domain.Departures]{Data: domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}, Source: "vbb", Realtime: true}}, nil
}

func fixture(t *testing.T, p *fake) (*Service, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 10, 18, 16, 30, 0, 0, time.UTC)}
	s, err := newService(context.Background(), p, Config{StationsTTL: 24 * time.Hour, DeparturesTTL: 30 * time.Second, MaxStale: 5 * time.Minute, Timeout: 5 * time.Second, CacheEntries: 16, CacheBytes: 1 << 20, RequestsPerMinute: 30}, c.Now, func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.Add(d)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, c
}

func awaitWaiters(t *testing.T, s *Service, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := s.waiters
		s.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not reach %d waiters", n)
}

func TestCacheCanonicalizationAndIsolation(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	first, err := s.SearchStations(context.Background(), "  Alex  ", 10)
	if err != nil {
		t.Fatal(err)
	}
	*first.Stations[0].Name = "changed"
	second, err := s.SearchStations(context.Background(), "Alex", 10)
	if err != nil || *second.Stations[0].Name != "Station" || p.calls.Load() != 1 || second.Stale || second.LastSuccessAt != nil {
		t.Fatalf("%+v %v", second, err)
	}
	c.Add(2 * time.Second)
	base, err := s.Departures(context.Background(), "opaque:001", domain.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Departures(context.Background(), "opaque:001", domain.Options{Mode: "suburban", Limit: 10, Duration: 30})
	if err != nil || p.calls.Load() != 2 || base.FetchedAt.Location().String() != "Europe/Berlin" {
		t.Fatal("equivalent options missed cache")
	}
	for _, opts := range []domain.Options{{Mode: "bus"}, {Limit: 5}, {Duration: 10}, {Direction: "dest:002"}} {
		c.Add(2 * time.Second)
		if _, err := s.Departures(context.Background(), "opaque:001", opts); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls.Load() != 6 {
		t.Fatal("result-affecting parameter missing from key")
	}
	if _, err := s.SearchStations(context.Background(), "", 1); !errors.Is(err, domain.ErrInput) {
		t.Error(err)
	}
	if _, err := s.Departures(context.Background(), "../bad", domain.Options{}); !errors.Is(err, domain.ErrInput) {
		t.Error(err)
	}
}

func TestStaleBoundsAndFailures(t *testing.T) {
	for _, kind := range []string{"transient", "404", "invalid_response", "no_stale", "max_age"} {
		t.Run(kind, func(t *testing.T) {
			p := &fake{}
			s, c := fixture(t, p)
			ctx := context.Background()
			start := c.Now()
			p.depart = func(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error) {
				return provider.Result[domain.Departures]{Data: domain.Departures{Station: domain.Station{ID: "id"}, Departures: []domain.Departure{}, Source: "vbb", Realtime: true}, Policy: provider.Policy{NoStale: kind == "no_stale"}}, nil
			}
			if _, err := s.Departures(ctx, "id", domain.Options{}); err != nil {
				t.Fatal(err)
			}
			c.Add(31 * time.Second)
			if kind == "max_age" {
				c.Add(5 * time.Minute)
			}
			p.depart = func(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error) {
				e := &provider.Error{Status: 503}
				if kind == "404" {
					e.Status = 404
				}
				if kind == "invalid_response" {
					e.Status = 0
					e.Code = "invalid_response"
				}
				return provider.Result[domain.Departures]{}, e
			}
			out, err := s.Departures(ctx, "id", domain.Options{})
			if kind == "transient" {
				if err != nil || !out.Stale || out.Realtime || out.LastSuccessAt == nil || !out.LastSuccessAt.Equal(start) || out.StaleAgeSeconds == nil || *out.StaleAgeSeconds != int64(c.Now().Sub(start)/time.Second) || !out.FetchedAt.Equal(start) {
					t.Fatalf("%+v %v", out, err)
				}
			} else if err == nil {
				t.Fatal("ineligible snapshot returned")
			}
			if kind == "404" && s.cache.Len() != 0 {
				t.Error("404 did not invalidate cache")
			}
		})
	}
}

func TestSearchStaleAndNoInitialSuccess(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	s.cfg.StationsTTL = time.Second
	if _, err := s.SearchStations(context.Background(), "Alex", 1); err != nil {
		t.Fatal(err)
	}
	c.Add(2 * time.Second)
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		return provider.Result[[]domain.Station]{}, &provider.Error{Code: "unavailable"}
	}
	out, err := s.SearchStations(context.Background(), "Alex", 1)
	if err != nil || !out.Stale || out.LastSuccessAt == nil || out.StaleAgeSeconds == nil {
		t.Fatalf("%+v %v", out, err)
	}
	c.Add(2 * time.Second)
	if _, err := s.SearchStations(context.Background(), "new", 1); err == nil {
		t.Fatal("served stale without any successful fetch")
	}
}

func TestCachePolicyAndRevalidation(t *testing.T) {
	for _, kind := range []string{"max_age", "no_store", "no_cache", "304", "304_no_store"} {
		t.Run(kind, func(t *testing.T) {
			p := &fake{}
			s, c := fixture(t, p)
			var calls int
			p.search = func(_ context.Context, _ string, _ int, etag string) (provider.Result[[]domain.Station], error) {
				calls++
				d := time.Second
				policy := provider.Policy{ETag: `"v1"`}
				if kind == "max_age" {
					policy.MaxAge = &d
				}
				if kind == "no_store" {
					policy.NoStore = true
				}
				if kind == "no_cache" {
					policy.Revalidate = true
					policy.NoStale = true
				}
				if calls > 1 && (kind == "304" || kind == "304_no_store") {
					if etag != `"v1"` {
						t.Errorf("lost validator: %q", etag)
					}
					return provider.Result[[]domain.Station]{NotModified: true, Policy: provider.Policy{NoStore: kind == "304_no_store"}}, nil
				}
				if kind == "no_store" && etag != "" {
					t.Error("no-store retained validator")
				}
				return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "id"}}, Policy: policy}, nil
			}
			first, err := s.SearchStations(context.Background(), "Alex", 1)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "304" || kind == "304_no_store" {
				c.Add(24 * time.Hour)
			} else {
				c.Add(2 * time.Second)
			}
			second, err := s.SearchStations(context.Background(), "Alex", 1)
			if err != nil || second.Stale || len(second.Stations) != 1 || second.FetchedAt.Equal(first.FetchedAt) || calls != 2 {
				t.Fatalf("%+v calls=%d %v", second, calls, err)
			}
			if (kind == "no_store" || kind == "304_no_store") && s.cache.Len() != 0 {
				t.Error("no-store retained snapshot")
			}
		})
	}
}

func TestBudgetRetryAndCooldown(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	if _, err := s.SearchStations(context.Background(), "first", 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.SearchStations(context.Background(), "second", 1)
	var limited *Error
	if !errors.As(err, &limited) || limited.Code != "rate_limited" || limited.RetryAfter != 2*time.Second || p.calls.Load() != 1 {
		t.Fatalf("%v", err)
	}
	c.Add(2 * time.Second)
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		return provider.Result[[]domain.Station]{}, &provider.Error{Status: 429, RetryAt: c.Now().Add(10 * time.Second)}
	}
	if _, err := s.SearchStations(context.Background(), "second", 1); err == nil {
		t.Error("missing upstream error")
	}
	_, err = s.Departures(context.Background(), "id", domain.Options{})
	if !errors.As(err, &limited) || limited.RetryAfter != 10*time.Second {
		t.Fatalf("cooldown ignored: %v", err)
	}
	c.Add(10 * time.Second)
	attempts := 0
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		attempts++
		if attempts == 1 {
			return provider.Result[[]domain.Station]{}, &provider.Error{Status: 502}
		}
		return provider.Result[[]domain.Station]{Data: []domain.Station{}}, nil
	}
	if _, err := s.SearchStations(context.Background(), "second", 1); err != nil || attempts != 2 {
		t.Fatalf("attempts %d: %v", attempts, err)
	}
}

func TestCoalescingAndIndependentCancellation(t *testing.T) {
	p := &fake{}
	s, _ := fixture(t, p)
	release := make(chan struct{})
	p.search = func(ctx context.Context, _ string, _ int, _ string) (provider.Result[[]domain.Station], error) {
		select {
		case <-ctx.Done():
			return provider.Result[[]domain.Station]{}, ctx.Err()
		case <-release:
			return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "id"}}}, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := s.SearchStations(ctx, "same", 1); first <- err }()
	awaitWaiters(t, s, 1)
	done := make(chan error, 12)
	for range 12 {
		go func() { _, err := s.SearchStations(context.Background(), "same", 1); done <- err }()
	}
	awaitWaiters(t, s, 13)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	for range 12 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if p.calls.Load() != 1 {
		t.Fatalf("made %d identical upstream requests", p.calls.Load())
	}
}

func TestBoundedWaitersAndShutdown(t *testing.T) {
	p := &fake{}
	s, _ := fixture(t, p)
	p.search = func(ctx context.Context, _ string, _ int, _ string) (provider.Result[[]domain.Station], error) {
		<-ctx.Done()
		return provider.Result[[]domain.Station]{}, ctx.Err()
	}
	done := make(chan error, 128)
	for range 128 {
		go func() { _, err := s.SearchStations(context.Background(), "same", 1); done <- err }()
	}
	awaitWaiters(t, s, 128)
	_, err := s.SearchStations(context.Background(), "same", 1)
	var overloaded *Error
	if !errors.As(err, &overloaded) || overloaded.Code != "overloaded" {
		t.Fatal(err)
	}
	s.Close()
	for range 128 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown: %v", err)
		}
	}
	if _, err := s.SearchStations(context.Background(), "same", 1); err == nil {
		t.Error("closed service accepted request")
	}
}

func TestBoundedDistinctFlights(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	entered := make(chan struct{}, 16)
	p.search = func(ctx context.Context, _ string, _ int, _ string) (provider.Result[[]domain.Station], error) {
		entered <- struct{}{}
		<-ctx.Done()
		return provider.Result[[]domain.Station]{}, ctx.Err()
	}
	done := make(chan error, 16)
	for i := range 16 {
		go func() { _, err := s.SearchStations(context.Background(), fmt.Sprint(i), 1); done <- err }()
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("provider not called")
		}
		c.Add(2 * time.Second)
	}
	_, err := s.SearchStations(context.Background(), "extra", 1)
	var e *Error
	if !errors.As(err, &e) || e.Code != "overloaded" {
		t.Fatal(err)
	}
	s.Close()
	for range 16 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}
}

func TestCancellationAndConfiguration(t *testing.T) {
	p := &fake{}
	s, _ := fixture(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.SearchStations(ctx, "test", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), p, Config{}); err == nil {
		t.Error("accepted invalid config")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := wait(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	s.cfg.MaxStale = 0
	if _, err := s.Departures(context.Background(), "id", domain.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestAllCallersCancelUpstream(t *testing.T) {
	p := &fake{}
	s, _ := fixture(t, p)
	canceled := make(chan struct{})
	p.search = func(ctx context.Context, _ string, _ int, _ string) (provider.Result[[]domain.Station], error) {
		<-ctx.Done()
		close(canceled)
		return provider.Result[[]domain.Station]{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.SearchStations(ctx, "same", 1); done <- err }()
	awaitWaiters(t, s, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("abandoned upstream request was not canceled")
	}
}

func TestTimeoutMayUseEligibleStaleSnapshot(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	if _, err := s.Departures(context.Background(), "id", domain.Options{}); err != nil {
		t.Fatal(err)
	}
	c.Add(31 * time.Second)
	s.cfg.Timeout = 10 * time.Millisecond
	p.depart = func(ctx context.Context, _ string, _ domain.Options, _ string) (provider.Result[domain.Departures], error) {
		<-ctx.Done()
		return provider.Result[domain.Departures]{}, ctx.Err()
	}
	out, err := s.Departures(context.Background(), "id", domain.Options{})
	if err != nil || !out.Stale || out.Realtime {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestUnexpected304FailsClosed(t *testing.T) {
	p := &fake{}
	s, _ := fixture(t, p)
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		return provider.Result[[]domain.Station]{NotModified: true}, nil
	}
	_, err := s.SearchStations(context.Background(), "test", 1)
	var e *provider.Error
	if !errors.As(err, &e) || e.Code != "invalid_response" {
		t.Fatalf("%v", err)
	}
}

func Test304WithoutValidatorAndLateSuccess(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	s.cfg.StationsTTL = time.Second
	if _, err := s.SearchStations(context.Background(), "test", 1); err != nil {
		t.Fatal(err)
	}
	c.Add(2 * time.Second)
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		return provider.Result[[]domain.Station]{NotModified: true}, nil
	}
	_, err := s.SearchStations(context.Background(), "test", 1)
	var e *provider.Error
	if !errors.As(err, &e) || e.Code != "invalid_response" {
		t.Fatalf("304 without validator accepted: %v", err)
	}
	c.Add(2 * time.Second)
	s.cfg.Timeout = 10 * time.Millisecond
	p.search = func(ctx context.Context, _ string, _ int, _ string) (provider.Result[[]domain.Station], error) {
		<-ctx.Done()
		return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "late"}}}, nil
	}
	if _, err := s.SearchStations(context.Background(), "new", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late success ignored deadline: %v", err)
	}
}

func TestNormalizedPayloadBounds(t *testing.T) {
	p := &fake{}
	s, c := fixture(t, p)
	name := strings.Repeat("<", (2<<20)/6+1)
	p.search = func(context.Context, string, int, string) (provider.Result[[]domain.Station], error) {
		return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "id", Name: &name}}}, nil
	}
	_, err := s.SearchStations(context.Background(), "test", 1)
	var e *provider.Error
	if !errors.As(err, &e) || e.Code != "response_too_large" || s.cache.Len() != 0 {
		t.Fatalf("oversized normalized data accepted: %v", err)
	}
	c.Add(2 * time.Second)
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	p.depart = func(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error) {
		return provider.Result[domain.Departures]{Data: domain.Departures{Departures: []domain.Departure{{PlannedAt: &invalid}}}}, nil
	}
	_, err = s.Departures(context.Background(), "id", domain.Options{})
	if !errors.As(err, &e) || e.Code != "invalid_response" || s.cache.Len() != 0 {
		t.Fatalf("invalid normalized data accepted: %v", err)
	}
}

func TestCachedDeparturesNeverFetches(t *testing.T) {
	for _, policy := range []provider.Policy{{}, {NoStore: true}, {Revalidate: true, NoStale: true}, {NoStale: true}} {
		p := &fake{depart: func(_ context.Context, id string, _ domain.Options, _ string) (provider.Result[domain.Departures], error) {
			return provider.Result[domain.Departures]{Data: domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}, Realtime: true}, Policy: policy}, nil
		}}
		s, c := fixture(t, p)
		if _, ok := s.CachedDepartures("id", domain.Options{}); ok {
			t.Fatal("no history")
		}
		if _, err := s.Departures(context.Background(), "id", domain.Options{}); err != nil {
			t.Fatal(err)
		}
		d, ok := s.CachedDepartures("id", domain.Options{})
		if ok == (policy.NoStore || policy.Revalidate) {
			t.Fatal("cache restrictions")
		}
		if ok && (d.Stale || !d.Realtime) {
			t.Fatal(d)
		}
		c.Add(31 * time.Second)
		d, ok = s.CachedDepartures("id", domain.Options{})
		if ok == (policy.NoStore || policy.Revalidate || policy.NoStale) {
			t.Fatal("stale restrictions")
		}
		if ok && (!d.Stale || d.Realtime || d.LastSuccessAt == nil || *d.StaleAgeSeconds != 31) {
			t.Fatal(d)
		}
		c.Add(5 * time.Minute)
		if _, ok := s.CachedDepartures("id", domain.Options{}); ok {
			t.Fatal("expired snapshot")
		}
		if _, ok := s.CachedDepartures("../", domain.Options{}); ok {
			t.Fatal("invalid ID")
		}
		if _, ok := s.CachedDepartures("id", domain.Options{Mode: "bus"}); ok {
			t.Fatal("wrong cache key")
		}
		s.Close()
		if _, ok := s.CachedDepartures("id", domain.Options{}); ok {
			t.Fatal("closed service")
		}
		if p.calls.Load() != 1 {
			t.Fatal("cache reader fetched", p.calls.Load())
		}
	}
}
