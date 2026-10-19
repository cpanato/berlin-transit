package vbb

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

func TestConditionalRequests(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"version1"` {
			t.Error("missing validator")
		}
		w.Header().Set("ETag", `"version1"`)
		w.Header().Set("Cache-Control", "max-age=15, must-revalidate")
		w.Header().Set("Age", "5")
		w.WriteHeader(304)
	})
	r, err := c.Client.SearchStations(context.Background(), "test", 1, `"version1"`)
	if err != nil || !r.NotModified || r.Policy.ETag != `"version1"` || r.Policy.MaxAge == nil || *r.Policy.MaxAge != 10*time.Second || !r.Policy.NoStale {
		t.Fatalf("%+v %v", r, err)
	}
	d, err := c.Client.Departures(context.Background(), "test", domain.Options{}, `"version1"`)
	if err != nil || !d.NotModified {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestCachePolicies(t *testing.T) {
	for _, s := range []string{"no-store, no-cache", "max-age=invalid", "max-age=-1", "max-age=\"5\"", "max-age=0", "max-age=999999", "max-age=5"} {
		h := http.Header{}
		h.Set("Cache-Control", s)
		h.Set("Age", "6")
		h.Set("ETag", strings.Repeat("x", 513))
		p := cachePolicy(h)
		if p.ETag != "" {
			t.Error("unbounded validator")
		}
		if strings.Contains(s, "no-cache") && (!p.Revalidate || !p.NoStale || !p.NoStore) {
			t.Error("ignored restrictions")
		}
		if p.MaxAge != nil && (*p.MaxAge < 0 || *p.MaxAge > 24*time.Hour) {
			t.Error("unbounded freshness")
		}
	}
	if got := seconds("999999999999999999999"); got != 0 {
		t.Error(got)
	}

	h := http.Header{}
	h.Set("Cache-Control", "max-age=5, max-age=60")
	if p := cachePolicy(h); p.MaxAge == nil || *p.MaxAge != 5*time.Second {
		t.Fatal("ambiguous policy extended freshness")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		want  time.Time
	}{
		{"", time.Time{}}, {"invalid", time.Time{}}, {"-1", time.Time{}}, {"60", now.Add(time.Minute)}, {"0", now}, {"999999", now.Add(24 * time.Hour)},
		{now.Add(time.Hour).Format(http.TimeFormat), now.Add(time.Hour)}, {now.Add(48 * time.Hour).Format(http.TimeFormat), now.Add(24 * time.Hour)}, {now.Add(-time.Hour).Format(http.TimeFormat), time.Time{}},
	} {
		if got := retryAfter(tc.value, now); !got.Equal(tc.want) {
			t.Errorf("%q: got %v want %v", tc.value, got, tc.want)
		}
	}
}
