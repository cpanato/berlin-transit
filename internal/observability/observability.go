// Package observability keeps logs and optional counters free of location data.
package observability

import (
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

func Logger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

type Metrics struct{ requests, failures, nanoseconds atomic.Uint64 }

func (m *Metrics) Observe(status int, elapsed time.Duration) {
	m.requests.Add(1)
	if status >= 400 {
		m.failures.Add(1)
	}
	if elapsed > 0 {
		m.nanoseconds.Add(uint64(elapsed))
	}
}

// No per-station, query, address, user-agent or upstream URL labels are used.
func (m *Metrics) Write(w io.Writer) error {
	_, err := fmt.Fprintf(w, "# TYPE berlin_transit_requests_total counter\nberlin_transit_requests_total %d\n# TYPE berlin_transit_errors_total counter\nberlin_transit_errors_total %d\n# TYPE berlin_transit_request_duration_seconds summary\nberlin_transit_request_duration_seconds_sum %.9f\nberlin_transit_request_duration_seconds_count %d\n", m.requests.Load(), m.failures.Load(), float64(m.nanoseconds.Load())/float64(time.Second), m.requests.Load())
	return err
}
