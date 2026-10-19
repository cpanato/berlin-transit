// Package provider describes retrieval metadata without changing domain models.
package provider

import "time"

// Error contains only a sanitized category, status and bounded retry deadline.
type Error struct {
	Code    string
	Status  int
	RetryAt time.Time
}

func (e *Error) Error() string { return "provider: " + e.Code }

func (e *Error) Temporary() bool {
	return e.Code == "unavailable" || e.Code == "read" || e.Status == 429 || e.Status == 500 || e.Status == 502 || e.Status == 503 || e.Status == 504
}

// Policy describes upstream cache constraints. The service always applies its
// own TTL and retention bounds as upper limits. ETags are opaque validators.
type Policy struct {
	ETag       string
	MaxAge     *time.Duration
	NoStore    bool
	Revalidate bool
	NoStale    bool
}

type Result[T any] struct {
	Data        T
	Policy      Policy
	NotModified bool
}
