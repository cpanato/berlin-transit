package trmnl

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/cpanato/berlin-transit/internal/domain"
)

type Reader interface {
	CachedDepartures(string, domain.Options) (domain.Departures, bool)
}

type departure struct {
	Line         *string    `json:"line"`
	Direction    *string    `json:"direction"`
	PlannedAt    *time.Time `json:"planned_at"`
	PredictedAt  *time.Time `json:"predicted_at"`
	DelaySeconds *int       `json:"delay_seconds"`
	Cancelled    bool       `json:"cancelled"`
	Platform     *string    `json:"platform"`
}
type station struct {
	Station         domain.Station `json:"station"`
	Departures      []departure    `json:"departures"`
	Source          string         `json:"source"`
	CollectedAt     time.Time      `json:"collected_at"`
	Stale           bool           `json:"stale"`
	Realtime        bool           `json:"realtime"`
	LastSuccessAt   *time.Time     `json:"last_success_at,omitempty"`
	StaleAgeSeconds *int64         `json:"stale_age_seconds,omitempty"`
}
type variables struct {
	GeneratedAt         time.Time `json:"generated_at"`
	Stations            []station `json:"stations"`
	UnavailableStations []string  `json:"unavailable_stations"`
}
type payload struct {
	Variables variables `json:"merge_variables"`
}

func build(reader Reader, ids []string, opts domain.Options, limit int, now time.Time) ([]byte, [32]byte, error) {
	p := payload{Variables: variables{Stations: []station{}, UnavailableStations: []string{}}}
	for _, id := range ids {
		d, ok := reader.CachedDepartures(id, opts)
		if !ok || d.FetchedAt.IsZero() {
			p.Variables.UnavailableStations = append(p.Variables.UnavailableStations, id)
			continue
		}
		group := station{Station: d.Station, Departures: []departure{}, Source: d.Source, CollectedAt: d.FetchedAt, Stale: d.Stale, Realtime: d.Realtime && !d.Stale, LastSuccessAt: d.LastSuccessAt, StaleAgeSeconds: d.StaleAgeSeconds}
		for _, v := range d.Departures[:min(limit, len(d.Departures))] {
			group.Departures = append(group.Departures, departure{Line: v.Line, Direction: v.Direction, PlannedAt: v.PlannedAt, PredictedAt: v.PredictedAt, DelaySeconds: v.DelaySeconds, Cancelled: v.Cancelled, Platform: v.Platform})
		}
		p.Variables.Stations = append(p.Variables.Stations, group)
	}
	if len(p.Variables.Stations) == 0 {
		return nil, [32]byte{}, errors.New("no_snapshot")
	}
	// Exclude only generation time from deduplication, retaining collection time
	// and freshness so a refreshed or newly stale snapshot gets published.
	canonical, err := json.Marshal(p)
	if err != nil {
		return nil, [32]byte{}, errors.New("invalid_snapshot")
	}
	fingerprint := sha256.Sum256(canonical)
	p.Variables.GeneratedAt = now
	b, err := json.Marshal(p)
	if err != nil {
		return nil, [32]byte{}, errors.New("invalid_snapshot")
	}
	if len(b) > maxPayload {
		return nil, [32]byte{}, errors.New("payload_too_large")
	}
	return b, fingerprint, nil
}
