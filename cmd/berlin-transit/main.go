// berlin-transit collects JSON on demand or explicitly serves a loopback API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cpanato/berlin-transit/internal/config"
	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/observability"
	"github.com/cpanato/berlin-transit/internal/provider"
	"github.com/cpanato/berlin-transit/internal/provider/vbb"
)

// Define the provider interface where it is consumed.
type collectorProvider interface {
	SearchStations(context.Context, string, int, string) (provider.Result[[]domain.Station], error)
	Departures(context.Context, string, domain.Options, string) (provider.Result[domain.Departures], error)
}

func collect(ctx context.Context, p collectorProvider, args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: berlin-transit stations|departures [flags] | serve | healthcheck")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard) // flag errors can contain sensitive input.
	limit := f.Int("limit", 10, "maximum results (1–50)")
	var result any
	switch args[0] {
	case "stations":
		query := f.String("query", "", "station search text")
		if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
			return errors.New("usage: berlin-transit stations -query TEXT [-limit 10]")
		}
		s, err := p.SearchStations(ctx, *query, *limit, "")
		if err != nil {
			return err
		}
		result = s.Data
	case "departures":
		id := f.String("station", "", "opaque provider station ID")
		mode := f.String("mode", "suburban", "transport product")
		duration := f.Int("duration", 30, "lookahead in minutes (1–120)")
		direction := f.String("direction", "", "provider stop ID for direction")
		if err := f.Parse(args[1:]); err != nil || f.NArg() != 0 {
			return errors.New("usage: berlin-transit departures -station ID [-mode suburban] [-limit 10] [-duration 30] [-direction ID]")
		}
		if *limit == 0 || *duration == 0 {
			return vbb.ErrInput
		}
		d, err := p.Departures(ctx, *id, domain.Options{Mode: *mode, Limit: *limit, Duration: *duration, Direction: *direction}, "")
		if err != nil {
			return err
		}
		result = d.Data
	default:
		return errors.New("usage: berlin-transit stations|departures [flags] | serve | healthcheck")
	}
	return json.NewEncoder(w).Encode(result)
}

func run(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "healthcheck" {
		if len(args) != 1 {
			return errors.New("usage: berlin-transit healthcheck")
		}
		return healthcheck(ctx, cfg.ListenAddr)
	}
	c, err := vbb.New(cfg.BaseURL, cfg.HTTPTimeout)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(args) > 0 && args[0] == "serve" {
		if len(args) != 1 {
			return errors.New("usage: berlin-transit serve (configuration via environment)")
		}
		return serve(ctx, cfg, c, observability.Logger(os.Stderr, cfg.LogLevel))
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.HTTPTimeout)
	defer cancel()
	return collect(ctx, c, args, stdout)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
