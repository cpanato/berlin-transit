// berlin-transit collects normalized JSON on demand. It opens no listener.
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
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/provider/vbb"
)

// Define the provider interface where it is consumed.
type provider interface {
	SearchStations(context.Context, string, int) ([]domain.Station, error)
	Departures(context.Context, string, domain.Options) (domain.Departures, error)
}

func collect(ctx context.Context, p provider, args []string, w io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: berlin-transit stations|departures [flags]")
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
		s, err := p.SearchStations(ctx, *query, *limit)
		if err != nil {
			return err
		}
		result = s
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
		d, err := p.Departures(ctx, *id, domain.Options{Mode: *mode, Limit: *limit, Duration: *duration, Direction: *direction})
		if err != nil {
			return err
		}
		result = d
	default:
		return errors.New("usage: berlin-transit stations|departures [flags]")
	}
	return json.NewEncoder(w).Encode(result)
}

func run(ctx context.Context, args []string, stdout io.Writer, getenv func(string) string) error {
	base := getenv("BERLIN_TRANSIT_VBB_BASE_URL")
	if base == "" {
		base = vbb.Origin
	}
	timeout := 10 * time.Second
	if v := getenv("BERLIN_TRANSIT_HTTP_TIMEOUT"); v != "" {
		var err error
		timeout, err = time.ParseDuration(v)
		if err != nil {
			return errors.New("invalid BERLIN_TRANSIT_HTTP_TIMEOUT")
		}
	}
	c, err := vbb.New(base, timeout)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
