package observability

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, errors.New("broken") }

func TestLoggingAndMetrics(t *testing.T) {
	var b bytes.Buffer
	l := Logger(&b, slog.LevelInfo)
	l.Debug("hidden")
	l.Info("request", "request_id", "123")
	if strings.Contains(b.String(), "hidden") || !strings.Contains(b.String(), `"request_id":"123"`) {
		t.Fatal(b.String())
	}
	b.Reset()
	var m Metrics
	m.Observe(200, time.Second)
	m.Observe(503, 2*time.Second)
	m.Observe(400, -1)
	if err := m.Write(&b); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"requests_total 3", "errors_total 2", "seconds_sum 3.000000000", "seconds_count 3"} {
		if !strings.Contains(b.String(), s) {
			t.Errorf("missing %s", s)
		}
	}
	if err := m.Write(broken{}); err == nil {
		t.Fatal("ignored writer error")
	}
}
