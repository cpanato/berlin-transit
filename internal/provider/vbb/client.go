// Package vbb implements the third-party v6.vbb.transport.rest contract.
package vbb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
	_ "time/tzdata" // Europe/Berlin also works on hosts without a timezone database.

	"github.com/cpanato/berlin-transit/internal/domain"
	"github.com/cpanato/berlin-transit/internal/netguard"
	"github.com/cpanato/berlin-transit/internal/provider"
)

const Origin = "https://v6.vbb.transport.rest"
const maxBody = 2 << 20

var ErrInput = domain.ErrInput

// Error deliberately excludes request URLs and upstream response bodies.
type Error = provider.Error

type Client struct {
	base string
	http *http.Client
	now  func() time.Time
	zone *time.Location
}

// New permits only the allowlisted HTTPS origin. Redirects and proxies are
// disabled; DNS answers are checked and the validated IP is dialed directly.
func New(base string, timeout time.Duration) (*Client, error) {
	if base != Origin && base != Origin+"/" {
		return nil, errors.New("unsupported VBB origin")
	}
	if timeout <= 0 || timeout > time.Minute {
		return nil, errors.New("timeout must be in (0, 1m]")
	}
	t := &http.Transport{
		DialContext: publicDial, MaxConnsPerHost: 4, MaxIdleConns: 4,
		MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: timeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return newClient(Origin, &http.Client{Transport: t, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, time.Now)
}

func publicDial(ctx context.Context, network, addr string) (net.Conn, error) {
	return netguard.DialContext(ctx, network, addr, "v6.vbb.transport.rest")
}

// Test construction is deliberately private; production has no unsafe URL override.
func newClient(base string, hc *http.Client, now func() time.Time) (*Client, error) {
	zone, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, err
	}
	return &Client{base: base, http: hc, now: now, zone: zone}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) get(ctx context.Context, path string, q url.Values, etag string, out any) (provider.Policy, bool, error) {
	var empty provider.Policy
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return empty, false, &Error{Code: "request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "berlin-transit/0.2")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return empty, false, ctx.Err()
		}
		return empty, false, &Error{Code: "unavailable"}
	}
	defer func() { _ = resp.Body.Close() }()
	policy := cachePolicy(resp.Header)
	if resp.StatusCode == http.StatusNotModified {
		if etag == "" {
			return empty, false, &Error{Code: "invalid_response"}
		}
		return policy, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return empty, false, &Error{Code: "upstream_status", Status: resp.StatusCode, RetryAt: retryAfter(resp.Header.Get("Retry-After"), c.now())}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return empty, false, ctx.Err()
		}
		return empty, false, &Error{Code: "read"}
	}
	if len(body) > maxBody {
		return empty, false, &Error{Code: "response_too_large"}
	}
	if json.Unmarshal(body, out) != nil {
		return empty, false, &Error{Code: "invalid_response"}
	}
	return policy, false, nil
}

func (c *Client) SearchStations(ctx context.Context, query string, limit int, etag string) (provider.Result[[]domain.Station], error) {
	var zero provider.Result[[]domain.Station]
	query, err := domain.SearchQuery(query, limit)
	if err != nil {
		return zero, err
	}
	var raw []struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
		Type string  `json:"type"`
	}
	policy, notModified, err := c.get(ctx, "/locations", url.Values{"query": {query}, "results": {strconv.Itoa(limit)}, "stops": {"true"}, "addresses": {"false"}, "poi": {"false"}}, etag, &raw)
	if err != nil {
		return zero, err
	}
	if notModified {
		return provider.Result[[]domain.Station]{Policy: policy, NotModified: true}, nil
	}
	if raw == nil {
		return zero, &Error{Code: "invalid_response"}
	}
	out := make([]domain.Station, 0, len(raw))
	for _, s := range raw {
		if s.Type != "stop" && s.Type != "station" {
			continue
		}
		if !domain.ValidID(s.ID) {
			return zero, &Error{Code: "invalid_response"}
		}
		out = append(out, domain.Station{ID: s.ID, Name: s.Name})
		if len(out) == limit {
			break
		}
	}
	return provider.Result[[]domain.Station]{Data: out, Policy: policy}, nil
}

type departure struct {
	Stop struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
	} `json:"stop"`
	Line struct {
		Name    *string `json:"name"`
		Product *string `json:"product"`
	} `json:"line"`
	Direction       *string    `json:"direction"`
	When            *time.Time `json:"when"`
	PlannedWhen     *time.Time `json:"plannedWhen"`
	Delay           *int       `json:"delay"`
	Platform        *string    `json:"platform"`
	PlannedPlatform *string    `json:"plannedPlatform"`
	Cancelled       bool       `json:"cancelled"`
	Provenance      *string    `json:"provenance"`
}

func (c *Client) Departures(ctx context.Context, id string, opts domain.Options, etag string) (provider.Result[domain.Departures], error) {
	var zero provider.Result[domain.Departures]
	opts, err := domain.NormalizeOptions(id, opts)
	if err != nil {
		return zero, err
	}
	q := url.Values{"results": {strconv.Itoa(opts.Limit)}, "duration": {strconv.Itoa(opts.Duration)}, "remarks": {"false"}}
	for _, m := range domain.Modes {
		enabled := m == opts.Mode
		q.Set(m, strconv.FormatBool(enabled))
	}
	if opts.Direction != "" {
		q.Set("direction", opts.Direction)
	}
	var raw struct {
		Departures *[]departure `json:"departures"`
		Updated    *int64       `json:"realtimeDataUpdatedAt"`
	}
	policy, notModified, err := c.get(ctx, "/stops/"+url.PathEscape(id)+"/departures", q, etag, &raw)
	if err != nil {
		return zero, err
	}
	if notModified {
		return provider.Result[domain.Departures]{Policy: policy, NotModified: true}, nil
	}
	if raw.Departures == nil {
		return zero, &Error{Code: "invalid_response"}
	}
	out := domain.Departures{Station: domain.Station{ID: id}, Departures: make([]domain.Departure, 0), Source: "vbb", FetchedAt: c.now().In(c.zone)}
	if raw.Updated != nil {
		t := time.Unix(*raw.Updated, 0).In(c.zone)
		out.RealtimeUpdatedAt = &t
	}
	for _, d := range *raw.Departures {
		if d.Line.Product == nil || *d.Line.Product != opts.Mode {
			continue
		}
		predicted := d.When
		// HAFAS returns the timetable in when even without realtime data.
		// A known delay (including zero) is the evidence for a prediction.
		if d.Delay == nil || d.Cancelled {
			predicted = nil
		}
		out.Departures = append(out.Departures, domain.Departure{Line: d.Line.Name, Mode: d.Line.Product, Direction: d.Direction, PlannedAt: c.local(d.PlannedWhen), PredictedAt: c.local(predicted), DelaySeconds: d.Delay, Platform: d.Platform, PlannedPlatform: d.PlannedPlatform, Cancelled: d.Cancelled, Provenance: d.Provenance})
		if d.Stop.ID == id {
			out.Station.Name = d.Stop.Name
		}
	}
	sort.SliceStable(out.Departures, func(i, j int) bool {
		a, b := out.Departures[i], out.Departures[j]
		x, y := a.PredictedAt, b.PredictedAt
		if x == nil {
			x = a.PlannedAt
		}
		if y == nil {
			y = b.PlannedAt
		}
		return x != nil && (y == nil || x.Before(*y))
	})
	if len(out.Departures) > opts.Limit {
		out.Departures = out.Departures[:opts.Limit]
	}
	for _, d := range out.Departures {
		if d.PredictedAt != nil {
			out.Realtime = true
			break
		}
	}
	return provider.Result[domain.Departures]{Data: out, Policy: policy}, nil
}

func (c *Client) local(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.In(c.zone)
	return &v
}
