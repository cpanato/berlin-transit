package vbb

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/berlin-transit/internal/provider"
)

func cachePolicy(h http.Header) provider.Policy {
	p := provider.Policy{ETag: h.Get("ETag")}
	if len(p.ETag) > 512 {
		p.ETag = ""
	}
	for _, field := range strings.Split(h.Get("Cache-Control"), ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(field), "=")
		switch strings.ToLower(key) {
		case "no-store":
			p.NoStore = true
		case "no-cache":
			p.Revalidate = true
			p.NoStale = true
		case "must-revalidate":
			p.NoStale = true
		case "max-age":
			d := seconds(strings.Trim(value, "\""))
			// Invalid freshness information is treated conservatively.
			if p.MaxAge == nil || d < *p.MaxAge {
				p.MaxAge = &d
			}
		}
	}
	if p.MaxAge != nil {
		d := *p.MaxAge - seconds(h.Get("Age"))
		if d < 0 {
			d = 0
		}
		p.MaxAge = &d
	}
	return p
}

func seconds(s string) time.Duration {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0
	}
	if n > 86400 {
		n = 86400
	}
	return time.Duration(n) * time.Second
}

func retryAfter(s string, now time.Time) time.Time {
	if s == "" {
		return time.Time{}
	}
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		if n > 86400 {
			n = 86400
		}
		return now.Add(time.Duration(n) * time.Second)
	}
	if t, err := http.ParseTime(s); err == nil && t.After(now) {
		if t.After(now.Add(24 * time.Hour)) {
			return now.Add(24 * time.Hour)
		}
		return t
	}
	return time.Time{}
}
