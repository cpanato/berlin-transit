package provider

import "testing"

func TestErrors(t *testing.T) {
	for _, code := range []string{"unavailable", "read"} {
		e := &Error{Code: code}
		if !e.Temporary() || e.Error() != "provider: "+code {
			t.Errorf("%+v", e)
		}
	}
	for _, status := range []int{429, 500, 502, 503, 504} {
		if !(&Error{Status: status}).Temporary() {
			t.Error(status)
		}
	}
	for _, status := range []int{400, 401, 404, 304} {
		if (&Error{Status: status}).Temporary() {
			t.Error(status)
		}
	}
}
