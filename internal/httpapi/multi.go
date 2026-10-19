package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/cpanato/berlin-transit/internal/domain"
)

// Each result carries its own freshness metadata or a sanitized error. Results
// retain input order even though independent station reads run concurrently.
type stationResult struct {
	StationID         string             `json:"station_id"`
	Status            int                `json:"status"`
	Data              *domain.Departures `json:"data,omitempty"`
	Error             *apiError          `json:"error,omitempty"`
	RetryAfterSeconds int64              `json:"retry_after_seconds,omitempty"`
}

type multiResponse struct {
	Results []stationResult `json:"results"`
	Partial bool            `json:"partial"`
}

func (a *API) multiDepartures(w *response, r *http.Request) {
	q, err := query(r.URL.RawQuery, "stations", "mode", "limit", "duration", "direction")
	if err != nil {
		fail(w, 400, "invalid_request")
		return
	}
	ids := strings.Split(q.Get("stations"), ",")
	if len(ids) > 5 {
		fail(w, 400, "invalid_request")
		return
	}
	limit, e1 := number(q, "limit", 10, 50)
	duration, e2 := number(q, "duration", 30, 120)
	seen := make(map[string]bool, len(ids))
	var opts domain.Options
	for _, id := range ids {
		opts, err = domain.NormalizeOptions(id, domain.Options{Mode: q.Get("mode"), Limit: limit, Duration: duration, Direction: q.Get("direction")})
		if err != nil || e1 != nil || e2 != nil || seen[id] {
			fail(w, 400, "invalid_request")
			return
		}
		seen[id] = true
	}
	out := multiResponse{Results: make([]stationResult, len(ids))}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			out.Results[i] = a.station(r, id, opts)
		})
	}
	wg.Wait()
	successes := 0
	status := out.Results[0].Status
	var retryAfter int64
	for _, result := range out.Results {
		if result.Status == 200 {
			successes++
		}
		if result.Status != status {
			status = 503
		}
		retryAfter = max(retryAfter, result.RetryAfterSeconds)
	}
	out.Partial = successes > 0 && successes < len(ids)
	if successes > 0 {
		status = 200
	}
	if successes == 0 && retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
	}
	if successes < len(ids) {
		w.code = "station_errors"
	}
	writeJSON(w, status, out)
}

func (a *API) station(r *http.Request, id string, opts domain.Options) (result stationResult) {
	result = stationResult{StationID: id, Status: 200}
	defer func() {
		if recover() != nil {
			result.Status = 500
			result.Data = nil
			result.Error = &apiError{Code: "internal_error", Message: http.StatusText(500)}
		}
	}()
	data, err := a.collector.Departures(r.Context(), id, opts)
	if err != nil {
		status, code, delay := classify(err)
		result.Status = status
		result.Error = &apiError{Code: code, Message: http.StatusText(status)}
		if delay != 0 {
			result.RetryAfterSeconds = retrySeconds(delay)
		}
	} else {
		result.Data = &data
	}
	return result
}
