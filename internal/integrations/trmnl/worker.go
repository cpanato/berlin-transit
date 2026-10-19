package trmnl

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cpanato/berlin-transit/internal/collector"
	"github.com/cpanato/berlin-transit/internal/domain"
)

// Run sends at most one latest snapshot at a time. Retries happen on later
// ticks, never earlier than the normal interval, respecting the hourly budget.
// There is no backlog, disk spool, extra VBB fetch or inbound listener.
func Run(ctx context.Context, cfg Config, client *Client, reader Reader, ids []string, opts domain.Options, log *slog.Logger) {
	if !cfg.Enabled {
		return
	}
	run(ctx, cfg, client.send, reader, ids, opts, log, time.Now, collector.Wait, collector.Jitter)
}

func run(ctx context.Context, cfg Config, send func(context.Context, []byte) error, reader Reader, ids []string, opts domain.Options, log *slog.Logger, now func() time.Time, sleep func(context.Context, time.Duration) error, jitter func() time.Duration) {
	var last [32]byte
	haveLast := false
	var pending [32]byte
	attempts := 0
	next := time.Time{}
	for ctx.Err() == nil {
		if !now().Before(next) {
			b, hash, err := build(reader, ids, opts, cfg.MaxDepartures, now())
			if err != nil {
				log.Debug("trmnl_skipped", "error_code", err.Error())
			} else if !haveLast || hash != last {
				if hash != pending {
					pending = hash
					attempts = 0
				}
				if attempts < 3 {
					err = send(ctx, b)
					attempts++
					if err == nil {
						last = hash
						haveLast = true
						attempts = 0
						log.Info("trmnl_sent")
					} else if ctx.Err() == nil {
						log.Warn("trmnl_failed", "error_code", "delivery_unavailable")
						var e *sendError
						if !errors.As(err, &e) || !e.temporary {
							attempts = 3
						} else {
							next = now().Add(cfg.Interval * time.Duration(1<<(attempts-1)))
							if e.retryAt.After(next) {
								next = e.retryAt
							}
						}
					}
				}
			}
		}
		if sleep(ctx, cfg.Interval+jitter()) != nil {
			return
		}
	}
}
