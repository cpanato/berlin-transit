package trmnl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cpanato/berlin-transit/internal/netguard"
)

const maxPayload = 5000

type Client struct {
	url  string
	http *http.Client
	now  func() time.Time
}

type sendError struct {
	code      string
	temporary bool
	retryAt   time.Time
}

func (e *sendError) Error() string { return "TRMNL: " + e.code }

func NewClient(cfg Config) (*Client, error) {
	if !cfg.Enabled || cfg.Validate() != nil {
		return nil, errors.New("invalid TRMNL configuration")
	}
	t := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return netguard.DialContext(ctx, network, addr, "trmnl.com")
		},
		MaxConnsPerHost: 1, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16 << 10,
	}
	return &Client{url: cfg.WebhookURL, http: &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) send(ctx context.Context, payload []byte) error {
	if len(payload) > maxPayload {
		return &sendError{code: "payload_too_large"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return &sendError{code: "unavailable"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return &sendError{code: "unavailable", temporary: true}
	}
	defer func() { _ = resp.Body.Close() }()
	// Responses may repeat the secret URL or the entire submitted payload. Never
	// decode or log them; cap any discard and close larger responses immediately.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	e := &sendError{code: "rejected"}
	if resp.StatusCode == 429 || resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
		e.code = "unavailable"
		e.temporary = true
		e.retryAt = retryAt(resp.Header.Get("Retry-After"), c.now())
	}
	return e
}

func retryAt(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if len(raw) > 128 {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
		return now.Add(time.Duration(min(n, 86400)) * time.Second)
	}
	if t, err := http.ParseTime(raw); err == nil && t.After(now) {
		if t.After(now.Add(24 * time.Hour)) {
			return now.Add(24 * time.Hour)
		}
		return t
	}
	return time.Time{}
}
