// Package vbb implements the third-party v6.vbb.transport.rest contract.
package vbb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Berlin also works on hosts without a timezone database.
	"unicode"
	"unicode/utf8"

	"github.com/cpanato/berlin-transit/internal/domain"
)

const Origin = "https://v6.vbb.transport.rest"
const maxBody = 2 << 20

var ErrInput = errors.New("invalid collector input")

// Error deliberately excludes request URLs and upstream response bodies.
type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return "vbb: " + e.Code }

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

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		if netip.MustParsePrefix(p).Contains(ip) {
			return false
		}
	}
	return true
}

func publicDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "v6.vbb.transport.rest" || port != "443" {
		return nil, errors.New("blocked destination")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("DNS unavailable")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, errors.New("blocked destination")
		}
	}
	var dialer net.Dialer
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("connection unavailable")
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

func validText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func validID(s string) bool {
	return validText(s, 256) && strings.TrimSpace(s) == s && s != "." && s != ".." && !strings.ContainsAny(s, "/\\?#%") && !strings.ContainsFunc(s, unicode.IsSpace)
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return &Error{Code: "request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "berlin-transit/0.1")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Code: "unavailable"}
	}
	defer func() { _ = resp.Body.Close() }() // Read errors are handled below; closing cannot change the result.
	if resp.StatusCode != http.StatusOK {
		return &Error{Code: "upstream_status", Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return &Error{Code: "read"}
	}
	if len(body) > maxBody {
		return &Error{Code: "response_too_large"}
	}
	if json.Unmarshal(body, out) != nil {
		return &Error{Code: "invalid_response"}
	}
	return nil
}

func (c *Client) SearchStations(ctx context.Context, query string, limit int) ([]domain.Station, error) {
	query = strings.TrimSpace(query)
	if !validText(query, 200) || limit < 1 || limit > 50 {
		return nil, ErrInput
	}
	var raw []struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
		Type string  `json:"type"`
	}
	err := c.get(ctx, "/locations", url.Values{"query": {query}, "results": {strconv.Itoa(limit)}, "stops": {"true"}, "addresses": {"false"}, "poi": {"false"}}, &raw)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, &Error{Code: "invalid_response"}
	}
	out := make([]domain.Station, 0, len(raw))
	for _, s := range raw {
		if s.Type != "stop" && s.Type != "station" {
			continue
		}
		if !validID(s.ID) {
			return nil, &Error{Code: "invalid_response"}
		}
		out = append(out, domain.Station{ID: s.ID, Name: s.Name})
		if len(out) == limit {
			break
		}
	}
	return out, nil
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

var modes = []string{"suburban", "subway", "tram", "bus", "ferry", "express", "regional"}

func (c *Client) Departures(ctx context.Context, id string, opts domain.Options) (domain.Departures, error) {
	var zero domain.Departures
	if opts.Mode == "" {
		opts.Mode = "suburban"
	}
	if opts.Limit == 0 {
		opts.Limit = 10
	}
	if opts.Duration == 0 {
		opts.Duration = 30
	}
	if !validID(id) || opts.Limit < 1 || opts.Limit > 50 || opts.Duration < 1 || opts.Duration > 120 || (opts.Direction != "" && !validID(opts.Direction)) {
		return zero, ErrInput
	}
	q := url.Values{"results": {strconv.Itoa(opts.Limit)}, "duration": {strconv.Itoa(opts.Duration)}, "remarks": {"false"}}
	found := false
	for _, m := range modes {
		enabled := m == opts.Mode
		found = found || enabled
		q.Set(m, strconv.FormatBool(enabled))
	}
	if !found {
		return zero, fmt.Errorf("%w: unsupported mode", ErrInput)
	}
	if opts.Direction != "" {
		q.Set("direction", opts.Direction)
	}
	var raw struct {
		Departures *[]departure `json:"departures"`
		Updated    *int64       `json:"realtimeDataUpdatedAt"`
	}
	if err := c.get(ctx, "/stops/"+url.PathEscape(id)+"/departures", q, &raw); err != nil {
		return zero, err
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
	return out, nil
}

func (c *Client) local(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.In(c.zone)
	return &v
}
