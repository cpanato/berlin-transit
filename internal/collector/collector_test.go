package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

type source struct{ ids []string }

func (s *source) Departures(_ context.Context, id string, _ domain.Options) (domain.Departures, error) {
	s.ids = append(s.ids, id)
	return domain.Departures{}, errors.New("private provider body")
}

func TestValidation(t *testing.T) {
	good := Config{Enabled: true, Stations: []string{"a", "b"}, Interval: 30 * time.Second}
	if good.Validate(30) != nil {
		t.Fatal("valid configuration")
	}
	if (Config{}).Validate(0) != nil {
		t.Fatal("disabled")
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Stations = nil }, func(c *Config) { c.Stations = []string{"a", "b", "c", "d", "e", "f"} }, func(c *Config) { c.Stations = []string{"a", "a"} }, func(c *Config) { c.Stations = []string{"../"} }, func(c *Config) { c.Interval = time.Second }, func(c *Config) { c.Interval = 2 * time.Hour }, func(c *Config) { c.Options.Mode = "unknown" },
	} {
		c := good
		change(&c)
		if c.Validate(30) == nil {
			t.Fatal(c)
		}
	}
	for _, rpm := range []int{0, 61, 1} {
		if good.Validate(rpm) == nil {
			t.Fatal("invalid budget", rpm)
		}
	}
}

func TestRotationAndCancellation(t *testing.T) {
	s := &source{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := Config{Enabled: true, Stations: []string{"a", "b"}, Interval: 30 * time.Second}
	sleeps := 0
	sleep := func(_ context.Context, d time.Duration) error {
		if d != 15*time.Second+time.Millisecond {
			t.Fatal(d)
		}
		sleeps++
		if sleeps == 3 {
			cancel()
		}
		return nil
	}
	run(ctx, cfg, s, slog.New(slog.NewTextHandler(io.Discard, nil)), sleep, func() time.Duration { return time.Millisecond })
	if len(s.ids) != 3 || s.ids[0] != "a" || s.ids[1] != "b" || s.ids[2] != "a" {
		t.Fatal(s.ids)
	}
	// An interrupted wait exits without collecting another station.
	s.ids = nil
	run(context.Background(), cfg, s, slog.Default(), func(context.Context, time.Duration) error { return context.Canceled }, Jitter)
	if len(s.ids) != 1 {
		t.Fatal(s.ids)
	}
	Run(context.Background(), Config{}, nil, nil)
	if err := Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := Wait(context.Background(), time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	if j := Jitter(); j < 0 || j > 255*time.Millisecond {
		t.Fatal(j)
	}
}
