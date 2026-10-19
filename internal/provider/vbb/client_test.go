package vbb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
	"unicode/utf8"
)

var fixedNow = time.Date(2026, 10, 18, 16, 30, 0, 0, time.UTC)

func writeResponse(t *testing.T, w io.Writer, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write mock response: %v", err)
	}
}

// Preserve the original normalization assertions through a small test helper.
type testClient struct{ *Client }

func (c *testClient) SearchStations(ctx context.Context, q string, n int) ([]domain.Station, error) {
	r, e := c.Client.SearchStations(ctx, q, n, "")
	return r.Data, e
}
func (c *testClient) Departures(ctx context.Context, id string, o domain.Options) (domain.Departures, error) {
	r, e := c.Client.Departures(ctx, id, o, "")
	return r.Data, e
}

func mockClient(t *testing.T, h http.HandlerFunc) *testClient {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	hc := s.Client()
	hc.Timeout = time.Second
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c, err := newClient(s.URL, hc, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &testClient{c}
}

func TestSearchStations(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/locations" || q.Get("query") != "Münz & Straße" || q.Get("results") != "2" || q.Get("stops") != "true" || q.Get("poi") != "false" || q.Get("addresses") != "false" {
			t.Errorf("unexpected search request: %s", r.URL)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing Accept")
		}
		writeResponse(t, w, `[{"type":"stop","id":"opaque:001","name":"First"},{"type":"station","id":"002","name":null},{"type":"stop","id":"003"}]`)
	})
	out, err := c.SearchStations(context.Background(), "  Münz & Straße  ", 2)
	if err != nil || len(out) != 2 || out[0].ID != "opaque:001" || *out[0].Name != "First" || out[1].Name != nil {
		t.Fatalf("got %#v, %v", out, err)
	}
}

func TestDepartureNormalization(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/stops/opaque:001/departures" || q.Get("results") != "10" || q.Get("duration") != "30" || q.Get("direction") != "dest:002" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		for _, mode := range domain.Modes {
			if q.Get(mode) != fmt.Sprint(mode == "suburban") {
				t.Errorf("wrong filter %s", mode)
			}
		}
		writeResponse(t, w, `{"departures":[
		{"stop":{"id":"opaque:001","name":"Station"},"line":{"name":"S7","mode":"train","product":"suburban"},"direction":"Potsdam","plannedWhen":"2026-10-18T16:35:00Z","when":"2026-10-18T16:38:00Z","delay":180,"platform":"2","plannedPlatform":"1","provenance":"live"},
		{"line":{"name":"S3","product":"suburban"},"plannedWhen":"2026-10-18T18:31:00+02:00","when":"2026-10-18T18:31:00+02:00","delay":0},
		{"line":{"name":"S9","product":"suburban"},"plannedWhen":"2026-10-18T18:39:00+02:00","when":"2026-10-18T18:39:00+02:00","delay":null},
		{"line":{"product":"suburban"},"cancelled":true,"plannedWhen":"2026-10-18T18:40:00+02:00","when":null},
		{"line":{"product":"bus"},"delay":0,"when":"2026-10-18T18:30:00+02:00"}
		],"realtimeDataUpdatedAt":1792341000}`)
	})
	out, err := c.Departures(context.Background(), "opaque:001", domain.Options{Direction: "dest:002"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Departures) != 4 || out.Source != "vbb" || out.Stale || !out.Realtime || out.RealtimeUpdatedAt == nil || !out.FetchedAt.Equal(fixedNow) || *out.Station.Name != "Station" {
		t.Fatalf("bad result: %+v", out)
	}
	a, b, u, x := out.Departures[0], out.Departures[1], out.Departures[2], out.Departures[3]
	if *a.DelaySeconds != 0 || a.PredictedAt == nil || a.Platform != nil {
		t.Errorf("zero delay lost: %+v", a)
	}
	if *b.Line != "S7" || *b.Mode != "suburban" || *b.DelaySeconds != 180 || *b.Platform != "2" || *b.PlannedPlatform != "1" || *b.Direction != "Potsdam" || *b.Provenance != "live" || b.PlannedAt.Format(time.RFC3339) != "2026-10-18T18:35:00+02:00" {
		t.Errorf("bad normalized data: %+v", b)
	}
	if u.DelaySeconds != nil || u.PredictedAt != nil || !x.Cancelled || x.PredictedAt != nil {
		t.Error("unknown realtime or cancellation misrepresented")
	}
	bts, _ := json.Marshal(x)
	if !strings.Contains(string(bts), `"delay_seconds":null`) || !strings.Contains(string(bts), `"platform":null`) {
		t.Errorf("missing nulls: %s", bts)
	}
}

func TestDST(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `{"departures":[
	{"line":{"product":"suburban"},"plannedWhen":"2026-10-25T00:30:00Z"},
	{"line":{"product":"suburban"},"plannedWhen":"2026-10-25T01:30:00Z"},
	{"line":{"product":"suburban"},"plannedWhen":"2026-03-29T00:30:00Z"},
	{"line":{"product":"suburban"},"plannedWhen":"2026-03-29T01:30:00Z"}
	]}`)
	})
	out, err := c.Departures(context.Background(), "001", domain.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-03-29T01:30:00+01:00", "2026-03-29T03:30:00+02:00", "2026-10-25T02:30:00+02:00", "2026-10-25T02:30:00+01:00"}
	for i, d := range out.Departures {
		if got := d.PlannedAt.Format(time.RFC3339); got != want[i] {
			t.Errorf("got %s, want %s", got, want[i])
		}
	}
	if out.Realtime {
		t.Error("planned schedule claims realtime")
	}
}

func TestResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"malformed", "{", "invalid_response", 200},
		{"trailing", `{"departures":[]} {}`, "invalid_response", 200},
		{"missing", `{}`, "invalid_response", 200},
		{"null", `{"departures":null}`, "invalid_response", 200},
		{"invalid time", `{"departures":[{"when":"not a time"}]}`, "invalid_response", 200},
		{"oversized", strings.Repeat(" ", maxBody+1), "response_too_large", 200},
		{"429", "private upstream body", "upstream_status", 429},
		{"404", "private upstream body", "upstream_status", 404},
		{"500", "private upstream body", "upstream_status", 500},
		{"304", "", "invalid_response", 304},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(tc.status)
				writeResponse(t, w, tc.body)
			})
			_, err := c.Departures(context.Background(), "001", domain.Options{})
			var upstream *Error
			if !errors.As(err, &upstream) || upstream.Code != tc.code {
				t.Fatalf("got %v", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "001") {
				t.Fatal("error leaked data")
			}
		})
	}
}

func TestEmptyAndBoundedDepartures(t *testing.T) {
	for _, body := range []string{`{"departures":[]}`, `{"departures":[{"line":{"product":"suburban"}},{"line":{"product":"suburban"}},{"line":{}}]}`} {
		c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { writeResponse(t, w, body) })
		out, err := c.Departures(context.Background(), "001", domain.Options{Limit: 1})
		if err != nil || out.Departures == nil || len(out.Departures) > 1 || out.Station.Name != nil || out.Station.ID != "001" || out.Realtime {
			t.Fatalf("bad empty/limited result: %+v %v", out, err)
		}
	}
}

func TestRealtimeReflectsReturnedResults(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `{"departures":[
		{"line":{"product":"suburban"},"plannedWhen":"2026-10-18T18:30:00+02:00"},
		{"line":{"product":"suburban"},"when":"2026-10-18T18:35:00+02:00","delay":0}
		]}`)
	})
	out, err := c.Departures(context.Background(), "001", domain.Options{Limit: 1})
	if err != nil || len(out.Departures) != 1 || out.Realtime {
		t.Fatalf("discarded prediction affected realtime: %+v %v", out, err)
	}
}

func TestInputValidation(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid input reached upstream") })
	for _, id := range []string{"", ".", "..", "../secret", "a/b", "a\\b", "a?x", "a#x", "a%2fb", "a b", "a\n", strings.Repeat("x", 257)} {
		if _, err := c.Departures(context.Background(), id, domain.Options{}); !errors.Is(err, ErrInput) {
			t.Errorf("accepted ID %q: %v", id, err)
		}
	}
	for _, opts := range []domain.Options{{Limit: -1}, {Limit: 51}, {Duration: -1}, {Duration: 121}, {Mode: "train"}, {Direction: "a/b"}} {
		if _, err := c.Departures(context.Background(), "ok", opts); !errors.Is(err, ErrInput) {
			t.Errorf("accepted opts %+v", opts)
		}
	}
	for _, q := range []string{"", "  ", "a\x00b", string([]byte{0xff}), strings.Repeat("x", 201)} {
		if _, err := c.SearchStations(context.Background(), q, 10); !errors.Is(err, ErrInput) {
			t.Errorf("accepted query %q", q)
		}
	}
	for _, n := range []int{0, -1, 51} {
		if _, err := c.SearchStations(context.Background(), "ok", n); !errors.Is(err, ErrInput) {
			t.Error("accepted limit")
		}
	}
}

func TestSearchResponseValidation(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `[`, `[{"id":"../bad","type":"stop"}]`} {
		c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { writeResponse(t, w, body) })
		if _, err := c.SearchStations(context.Background(), "ok", 10); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, `[{"type":"address","id":"anything"}]`)
	})
	out, err := c.SearchStations(context.Background(), "ok", 10)
	if err != nil || len(out) != 0 {
		t.Fatalf("nonstation included: %v %v", out, err)
	}
}

func TestTransportSafety(t *testing.T) {
	for _, base := range []string{"http://v6.vbb.transport.rest", "https://127.0.0.1", "https://v6.vbb.transport.rest.evil", "https://user@v6.vbb.transport.rest", "https://v6.vbb.transport.rest/path", "https://v6.vbb.transport.rest?x=1", "https://v6.vbb.transport.rest:443"} {
		if _, err := New(base, time.Second); err == nil {
			t.Errorf("accepted %s", base)
		}
	}
	for _, d := range []time.Duration{0, -1, 2 * time.Minute} {
		if _, err := New(Origin, d); err == nil {
			t.Error("accepted timeout")
		}
	}
	for _, base := range []string{Origin, Origin + "/"} {
		c, err := New(base, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.1.2", "192.168.1.1", "169.254.1.1", "0.0.0.0", "224.0.0.1", "100.64.1.1", "198.18.0.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "255.255.255.255", "::1", "::", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted unsafe IP %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public IP %s", ip)
		}
	}
	for _, addr := range []string{"invalid", "127.0.0.1:443", "v6.vbb.transport.rest:80"} {
		if conn, err := publicDial(context.Background(), "tcp", addr); err == nil {
			_ = conn.Close()
			t.Error("accepted destination")
		}
	}
}

func TestRedirectTimeoutCancellation(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/private", http.StatusFound)
		})
		_, err := c.SearchStations(context.Background(), "ok", 1)
		var e *Error
		if !errors.As(err, &e) || e.Status != 302 {
			t.Fatalf("redirect followed: %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		c.http.Timeout = 10 * time.Millisecond
		_, err := c.SearchStations(context.Background(), "ok", 1)
		var e *Error
		if !errors.As(err, &e) || e.Code != "unavailable" {
			t.Fatalf("bad timeout: %v", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("canceled request reached upstream") })
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.SearchStations(ctx, "ok", 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestConcurrentClient(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { writeResponse(t, w, `{"departures":[]}`) })
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := c.Departures(context.Background(), "001", domain.Options{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func FuzzValidation(f *testing.F) {
	for _, s := range []string{"900100003", "de:11000:900100003", "../x", "", "Münzstraße", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if domain.ValidID(s) && ((!utf8.ValidString(s) || len(s) > 256) || strings.ContainsAny(s, "/\\?#%")) {
			t.Fatal("invalid path ID accepted")
		}
		_, _ = domain.SearchQuery(strings.TrimSpace(s), 10)
	})
}

func FuzzDepartureJSON(f *testing.F) {
	for _, s := range []string{`{"departures":[]}`, `{"departures":[{"delay":null,"when":null}]}`, `{`, `null`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var raw struct {
			Departures []departure `json:"departures"`
			Updated    *int64      `json:"realtimeDataUpdatedAt"`
		}
		_ = json.Unmarshal([]byte(s), &raw)
	})
}
