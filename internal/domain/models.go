// Package domain defines provider-independent collector data.
package domain

import "time"

type Station struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

type Options struct {
	Mode      string
	Limit     int
	Duration  int    // minutes
	Direction string // provider stop ID
}

type Departure struct {
	Line            *string    `json:"line"`
	Mode            *string    `json:"mode"`
	Direction       *string    `json:"direction"`
	PlannedAt       *time.Time `json:"planned_at"`
	PredictedAt     *time.Time `json:"predicted_at"`
	DelaySeconds    *int       `json:"delay_seconds"`
	Platform        *string    `json:"platform"`
	PlannedPlatform *string    `json:"planned_platform"`
	Cancelled       bool       `json:"cancelled"`
	Provenance      *string    `json:"provenance"`
}

type Departures struct {
	Station           Station     `json:"station"`
	Departures        []Departure `json:"departures"`
	Source            string      `json:"source"`
	FetchedAt         time.Time   `json:"fetched_at"`
	RealtimeUpdatedAt *time.Time  `json:"realtime_updated_at"`
	Realtime          bool        `json:"realtime"`
	Stale             bool        `json:"stale"`
}
