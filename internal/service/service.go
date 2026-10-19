// Package service coordinates provider access, caching and bounded concurrency.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/cpanato/berlin-transit/internal/cache"
	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/provider"
)

type Provider interface {
	SearchStations(context.Context, string, int, string) (provider.Result[[]domain.Station], error)
	Departures(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error)
}

type Config struct {
	StationsTTL, DeparturesTTL, MaxStale, Timeout time.Duration
	CacheEntries, CacheBytes, RequestsPerMinute   int
}

type Error struct {
	Code       string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return "service: " + e.Code }

type flight struct {
	done   chan struct{}
	cancel context.CancelFunc
	refs   int
	entry  cache.Entry
	stale  bool
	err    error
}

type Service struct {
	provider       Provider
	cache          *cache.Cache
	cfg            Config
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	zone           *time.Location
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	flights        map[string]*flight
	waiters        int
	next, cooldown time.Time
	wg             sync.WaitGroup
	closed         bool
}

func New(parent context.Context, p Provider, cfg Config) (*Service, error) {
	return newService(parent, p, cfg, time.Now, wait)
}

func newService(parent context.Context, p Provider, cfg Config, now func() time.Time, sleep func(context.Context, time.Duration) error) (*Service, error) {
	if p == nil || cfg.StationsTTL <= 0 || cfg.DeparturesTTL <= 0 || cfg.MaxStale < 0 || cfg.Timeout <= 0 || cfg.CacheEntries < 1 || cfg.CacheBytes < 1 || cfg.RequestsPerMinute < 1 || cfg.RequestsPerMinute > 60 {
		return nil, errors.New("invalid service configuration")
	}
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &Service{provider: p, cache: cache.New(cfg.CacheEntries, cfg.CacheBytes, now), cfg: cfg, now: now, sleep: sleep, zone: zone, ctx: ctx, cancel: cancel, flights: make(map[string]*flight)}, nil
}

func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock(); s.wg.Wait() }

func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func key(parts ...any) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// The budget has burst one and is shared by search, departures and retries.
func (s *Service) take() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	next := s.next
	if s.cooldown.After(next) {
		next = s.cooldown
	}
	if now.Before(next) {
		return &Error{Code: "rate_limited", RetryAfter: next.Sub(now)}
	}
	s.next = now.Add(time.Minute / time.Duration(s.cfg.RequestsPerMinute))
	return nil
}

type fetch func(context.Context, string) ([]byte, provider.Policy, bool, error)

// Normalized JSON can grow when strings need escaping. Bound it separately
// from the raw upstream body before retaining or sharing it with HTTP callers.
func snapshot(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &provider.Error{Code: "invalid_response"}
	}
	if len(b) > 2<<20 {
		return nil, &provider.Error{Code: "response_too_large"}
	}
	return b, nil
}

func (s *Service) retry(ctx context.Context, etag string, load fetch) ([]byte, provider.Policy, bool, error) {
	var empty provider.Policy
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, empty, false, err
		}
		if err := s.take(); err != nil {
			return nil, empty, false, err
		}
		b, p, notModified, err := load(ctx, etag)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			return b, p, notModified, nil
		}
		var upstream *provider.Error
		if !errors.As(err, &upstream) || !upstream.Temporary() {
			return nil, empty, false, err
		}
		if upstream.RetryAt.After(s.now()) {
			s.mu.Lock()
			if upstream.RetryAt.After(s.cooldown) {
				s.cooldown = upstream.RetryAt
			}
			s.mu.Unlock()
			return nil, empty, false, err
		}
		if attempt == 2 {
			return nil, empty, false, err
		}
		// At most two retries, with exponential backoff and jitter. Each also
		// consumes the shared budget, and may lose its slot to another request.
		var noise [1]byte
		_, _ = rand.Read(noise[:])
		delay := time.Minute/time.Duration(s.cfg.RequestsPerMinute) + (100 * time.Millisecond << attempt) + time.Duration(noise[0])*time.Millisecond
		if err := s.sleep(ctx, delay); err != nil {
			return nil, empty, false, err
		}
	}
	panic("unreachable")
}

func (s *Service) cached(ctx context.Context, k string, ttl time.Duration, load fetch) (cache.Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return cache.Entry{}, false, err
	}
	if s.ctx.Err() != nil {
		return cache.Entry{}, false, &Error{Code: "unavailable"}
	}
	if e, ok := s.cache.Get(k); ok && s.now().Before(e.FreshUntil) {
		return e, false, nil
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return cache.Entry{}, false, &Error{Code: "unavailable"}
	}
	if s.waiters >= 128 {
		s.mu.Unlock()
		return cache.Entry{}, false, &Error{Code: "overloaded"}
	}
	f, ok := s.flights[k]
	if !ok {
		if len(s.flights) >= 16 {
			s.mu.Unlock()
			return cache.Entry{}, false, &Error{Code: "overloaded"}
		}
		work, cancel := context.WithTimeout(s.ctx, s.cfg.Timeout)
		f = &flight{done: make(chan struct{}), cancel: cancel}
		s.flights[k] = f
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer cancel()
			e, stale, err := s.refresh(work, k, ttl, load)
			s.mu.Lock()
			f.entry = e
			f.stale = stale
			f.err = err
			delete(s.flights, k)
			close(f.done)
			s.mu.Unlock()
		}()
	}
	f.refs++
	s.waiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		f.refs--
		s.waiters--
		if f.refs == 0 {
			f.cancel()
		}
		s.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return cache.Entry{}, false, ctx.Err()
	case <-f.done:
		return f.entry, f.stale, f.err
	}
}

func (s *Service) refresh(ctx context.Context, k string, ttl time.Duration, load fetch) (cache.Entry, bool, error) {
	old, haveOld := s.cache.Get(k)
	// Another caller may have filled the cache before this flight started.
	if haveOld && s.now().Before(old.FreshUntil) {
		return old, false, nil
	}
	etag := ""
	if haveOld {
		etag = old.Policy.ETag
	}
	b, p, notModified, err := s.retry(ctx, etag, load)
	now := s.now()
	if err != nil {
		var upstream *provider.Error
		if errors.As(err, &upstream) && upstream.Status == 404 {
			s.cache.Delete(k)
			return cache.Entry{}, false, err
		}
		var local *Error
		transient := (errors.As(err, &upstream) && upstream.Temporary()) || (errors.As(err, &local) && local.Code == "rate_limited") || errors.Is(err, context.DeadlineExceeded)
		if haveOld && transient && !old.Policy.NoStale && now.Before(old.StoredAt.Add(s.cfg.MaxStale)) {
			return old, true, nil
		}
		return cache.Entry{}, false, err
	}
	if notModified {
		if !haveOld || etag == "" {
			return cache.Entry{}, false, &provider.Error{Code: "invalid_response"}
		}
		b = old.Value
		if p.ETag == "" {
			p.ETag = old.Policy.ETag
		}
		if p.MaxAge == nil {
			p.MaxAge = old.Policy.MaxAge
		}
		p.NoStale = p.NoStale || old.Policy.NoStale
		p.Revalidate = p.Revalidate || old.Policy.Revalidate
	}
	if p.MaxAge != nil && *p.MaxAge < ttl {
		ttl = *p.MaxAge
	}
	if p.Revalidate {
		ttl = 0
	}
	freshUntil := now.Add(ttl)
	retainUntil := freshUntil.Add(s.cfg.MaxStale)
	e := cache.Entry{Value: b, StoredAt: now, FreshUntil: freshUntil, RetainUntil: retainUntil, Policy: p}
	if p.NoStore {
		s.cache.Delete(k)
	} else {
		s.cache.Set(k, e)
	}
	return e, false, nil
}

func (s *Service) SearchStations(ctx context.Context, query string, limit int) (domain.Stations, error) {
	query, err := domain.SearchQuery(query, limit)
	if err != nil {
		return domain.Stations{}, err
	}
	e, stale, err := s.cached(ctx, key("vbb", "stations", query, limit), s.cfg.StationsTTL, func(ctx context.Context, etag string) ([]byte, provider.Policy, bool, error) {
		r, err := s.provider.SearchStations(ctx, query, limit, etag)
		if err != nil {
			return nil, r.Policy, false, err
		}
		b, err := snapshot(r.Data)
		return b, r.Policy, r.NotModified, err
	})
	if err != nil {
		return domain.Stations{}, err
	}
	out := domain.Stations{Source: "vbb", FetchedAt: e.StoredAt.In(s.zone), Stale: stale}
	if err := json.Unmarshal(e.Value, &out.Stations); err != nil {
		return domain.Stations{}, &Error{Code: "unavailable"}
	}
	if stale {
		t := e.StoredAt.In(s.zone)
		age := int64(s.now().Sub(e.StoredAt) / time.Second)
		out.LastSuccessAt = &t
		out.StaleAgeSeconds = &age
	}
	return out, nil
}

func (s *Service) Departures(ctx context.Context, id string, o domain.Options) (domain.Departures, error) {
	o, err := domain.NormalizeOptions(id, o)
	if err != nil {
		return domain.Departures{}, err
	}
	e, stale, err := s.cached(ctx, key("vbb", "departures", id, o), s.cfg.DeparturesTTL, func(ctx context.Context, etag string) ([]byte, provider.Policy, bool, error) {
		r, err := s.provider.Departures(ctx, id, o, etag)
		if err != nil {
			return nil, r.Policy, false, err
		}
		b, err := snapshot(r.Data)
		return b, r.Policy, r.NotModified, err
	})
	if err != nil {
		return domain.Departures{}, err
	}
	var out domain.Departures
	if err := json.Unmarshal(e.Value, &out); err != nil {
		return out, &Error{Code: "unavailable"}
	}
	out.FetchedAt = e.StoredAt.In(s.zone)
	out.Stale = stale
	if stale {
		t := e.StoredAt.In(s.zone)
		age := int64(s.now().Sub(e.StoredAt) / time.Second)
		out.LastSuccessAt = &t
		out.StaleAgeSeconds = &age
		out.Realtime = false
	}
	return out, nil
}
