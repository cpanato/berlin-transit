package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInput = errors.New("invalid collector input")

// ValidID validates one opaque provider path segment, without interpreting it.
func ValidID(s string) bool {
	return validText(s, 256) && s != "." && s != ".." &&
		!strings.ContainsAny(s, "/\\?#%") && !strings.ContainsFunc(s, unicode.IsSpace)
}

func validText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}

func SearchQuery(query string, limit int) (string, error) {
	query = strings.TrimSpace(query)
	if !validText(query, 200) || limit < 1 || limit > 50 {
		return "", ErrInput
	}
	return query, nil
}

var Modes = [...]string{"suburban", "subway", "tram", "bus", "ferry", "express", "regional"}

// NormalizeOptions makes equivalent requests share one cache key.
func NormalizeOptions(id string, o Options) (Options, error) {
	if o.Mode == "" {
		o.Mode = "suburban"
	}
	if o.Limit == 0 {
		o.Limit = 10
	}
	if o.Duration == 0 {
		o.Duration = 30
	}
	if !ValidID(id) || o.Limit < 1 || o.Limit > 50 || o.Duration < 1 || o.Duration > 120 ||
		(o.Direction != "" && !ValidID(o.Direction)) {
		return Options{}, ErrInput
	}
	for _, mode := range Modes {
		if o.Mode == mode {
			return o, nil
		}
	}
	return Options{}, ErrInput
}
