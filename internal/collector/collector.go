// Package collector periodically warms configured station caches, independently
// of HTTP handlers or output adapters. Collection is explicitly opt-in.
package collector

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

type Config struct {
	Enabled  bool
	Stations []string
	Interval time.Duration // minimum time for a full station cycle
	Options  domain.Options
}

func (c Config) Validate(requestsPerMinute int) error {
	if !c.Enabled {
		return nil
	}
	if len(c.Stations) < 1 || len(c.Stations) > 5 || c.Interval < 30*time.Second || c.Interval > time.Hour || requestsPerMinute < 1 || requestsPerMinute > 60 || c.Interval < time.Duration(len(c.Stations))*time.Minute/time.Duration(requestsPerMinute) {
		return errors.New("invalid collector configuration")
	}
	seen := map[string]bool{}
	for _, id := range c.Stations {
		if _, err := domain.NormalizeOptions(id, c.Options); err != nil || seen[id] {
			return errors.New("invalid collector configuration")
		}
		seen[id] = true
	}
	return nil
}

type Source interface {
	Departures(context.Context, string, domain.Options) (domain.Departures, error)
}

func Run(ctx context.Context, cfg Config, source Source, log *slog.Logger) {
	run(ctx, cfg, source, log, Wait, Jitter)
}

func run(ctx context.Context, cfg Config, source Source, log *slog.Logger, sleep func(context.Context, time.Duration) error, jitter func() time.Duration) {
	if !cfg.Enabled || len(cfg.Stations) == 0 {
		return
	}
	for i := 0; ctx.Err() == nil; i = (i + 1) % len(cfg.Stations) {
		if _, err := source.Departures(ctx, cfg.Stations[i], cfg.Options); err != nil && ctx.Err() == nil {
			log.Warn("collection_failed", "error_code", "collection_unavailable")
		}
		if sleep(ctx, cfg.Interval/time.Duration(len(cfg.Stations))+jitter()) != nil {
			return
		}
	}
}

// Wait and Jitter are shared scheduling primitives; no unbounded queues exist.
func Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func Jitter() time.Duration {
	var b [1]byte
	_, _ = rand.Read(b[:])
	return time.Duration(b[0]) * time.Millisecond
}
