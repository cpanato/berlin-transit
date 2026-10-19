package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/provider"
)

type fakeProvider struct {
	err   error
	query string
	limit int
	id    string
	opts  domain.Options
}

func (p *fakeProvider) SearchStations(_ context.Context, q string, n int, _ string) (provider.Result[[]domain.Station], error) {
	p.query = q
	p.limit = n
	return provider.Result[[]domain.Station]{Data: []domain.Station{{ID: "opaque:001"}}}, p.err
}
func (p *fakeProvider) Departures(_ context.Context, id string, o domain.Options, _ string) (provider.Result[domain.Departures], error) {
	p.id = id
	p.opts = o
	return provider.Result[domain.Departures]{Data: domain.Departures{Station: domain.Station{ID: id}, Departures: []domain.Departure{}}}, p.err
}

func TestCollect(t *testing.T) {
	ctx := context.Background()
	var b bytes.Buffer
	p := &fakeProvider{}
	if err := collect(ctx, p, []string{"stations", "-query", "Alexanderplatz", "-limit", "2"}, &b); err != nil {
		t.Fatal(err)
	}
	if p.query != "Alexanderplatz" || p.limit != 2 || !strings.Contains(b.String(), `"id":"opaque:001"`) {
		t.Fatal("bad search")
	}
	b.Reset()
	if err := collect(ctx, p, []string{"departures", "-station", "opaque:001", "-mode", "bus", "-limit", "3", "-duration", "15", "-direction", "dest"}, &b); err != nil {
		t.Fatal(err)
	}
	if p.id != "opaque:001" || p.opts.Mode != "bus" || p.opts.Limit != 3 || p.opts.Duration != 15 || p.opts.Direction != "dest" || !strings.Contains(b.String(), `"departures":[]`) {
		t.Fatal("bad departures")
	}
	if err := collect(ctx, p, []string{"departures", "-station", "001"}, &b); err != nil {
		t.Fatal(err)
	}
	if p.opts.Mode != "suburban" || p.opts.Limit != 10 || p.opts.Duration != 30 {
		t.Fatal("bad defaults")
	}
}

func TestCollectErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"stations", "-unknown", "sensitive"}, {"stations", "-limit", "invalid"}, {"stations", "extra"}, {"departures", "extra"}, {"departures", "-unknown", "sensitive"}, {"departures", "-limit", "0"}, {"departures", "-duration", "0"}} {
		var b bytes.Buffer
		err := collect(context.Background(), &fakeProvider{}, args, &b)
		if err == nil || b.Len() != 0 || strings.Contains(err.Error(), "sensitive") {
			t.Fatalf("bad error for %v: %v", args, err)
		}
	}
	sentinel := errors.New("provider unavailable")
	for _, cmd := range []string{"stations", "departures"} {
		if err := collect(context.Background(), &fakeProvider{err: sentinel}, []string{cmd}, &bytes.Buffer{}); !errors.Is(err, sentinel) {
			t.Fatalf("got %v", err)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestOutputError(t *testing.T) {
	if err := collect(context.Background(), &fakeProvider{}, []string{"stations"}, brokenWriter{}); err == nil {
		t.Fatal("ignored output error")
	}
}

func TestRunConfiguration(t *testing.T) {
	for _, env := range []map[string]string{
		{"BERLIN_TRANSIT_HTTP_TIMEOUT": "invalid secret"},
		{"BERLIN_TRANSIT_HTTP_TIMEOUT": "0s"},
		{"BERLIN_TRANSIT_HTTP_TIMEOUT": "61s"},
		{"BERLIN_TRANSIT_VBB_BASE_URL": "http://127.0.0.1/private"},
	} {
		var b bytes.Buffer
		err := run(context.Background(), []string{"stations", "-query", "x"}, &b, func(k string) string { return env[k] })
		if err == nil || b.Len() != 0 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatalf("unsafe configuration: %v", err)
		}
	}
	if err := run(context.Background(), nil, &bytes.Buffer{}, func(string) string { return "" }); err == nil {
		t.Fatal("missing usage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"stations", "-query", "x"}, &bytes.Buffer{}, func(string) string { return "" }); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
