package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidation(t *testing.T) {
	for _, id := range []string{"900100003", "opaque:001", "de:11000:900100003"} {
		if !ValidID(id) {
			t.Errorf("rejected %s", id)
		}
	}
	for _, id := range []string{"", ".", "..", "../x", "a/b", "a\\b", "a?x", "a#x", "a%2fb", "a b", "a\n", string([]byte{255}), strings.Repeat("a", 257)} {
		if ValidID(id) {
			t.Errorf("accepted %q", id)
		}
	}
	q, err := SearchQuery("  Münzstraße  ", 10)
	if err != nil || q != "Münzstraße" {
		t.Fatalf("%q %v", q, err)
	}
	for _, q := range []string{"", strings.Repeat("x", 201), "a\x00b"} {
		if _, err := SearchQuery(q, 1); !errors.Is(err, ErrInput) {
			t.Error("accepted search")
		}
	}
	for _, n := range []int{-1, 0, 51} {
		if _, err := SearchQuery("ok", n); err == nil {
			t.Error("accepted limit")
		}
	}
	o, err := NormalizeOptions("opaque:001", Options{})
	if err != nil || o.Mode != "suburban" || o.Limit != 10 || o.Duration != 30 {
		t.Fatalf("%+v %v", o, err)
	}
	for _, mode := range Modes {
		if _, err := NormalizeOptions("id", Options{Mode: mode, Direction: "dest:01", Limit: 50, Duration: 120}); err != nil {
			t.Error(err)
		}
	}
	for _, o := range []Options{{Mode: "train"}, {Limit: -1}, {Limit: 51}, {Duration: -1}, {Duration: 121}, {Direction: "a/b"}} {
		if _, err := NormalizeOptions("id", o); !errors.Is(err, ErrInput) {
			t.Errorf("accepted %+v", o)
		}
	}
	if _, err := NormalizeOptions("", Options{}); err == nil {
		t.Error("accepted missing ID")
	}
}
