package services

import (
	"net/http"
	"testing"
)

func TestParseRange(t *testing.T) {
	const size = 1000
	cases := []struct {
		rng    string
		begin  int64
		end    int64
		status int
	}{
		{"", 0, 999, http.StatusOK},
		{"bytes=0-", 0, 999, http.StatusPartialContent},
		{"bytes=0-999", 0, 999, http.StatusPartialContent},
		{"bytes=100-199", 100, 199, http.StatusPartialContent},
		{"bytes=100-", 100, 999, http.StatusPartialContent},
		// end past size is clamped, not rejected (RFC 7233 §2.1)
		{"bytes=100-5000", 100, 999, http.StatusPartialContent},
		// suffix range: last N bytes
		{"bytes=-500", 500, 999, http.StatusPartialContent},
		{"bytes=-5000", 0, 999, http.StatusPartialContent},
		{"bytes=-0", 0, 999, http.StatusRequestedRangeNotSatisfiable},
		// begin past the end
		{"bytes=1000-", 0, 999, http.StatusRequestedRangeNotSatisfiable},
		{"bytes=5000-6000", 0, 999, http.StatusRequestedRangeNotSatisfiable},
		// ignored → full 200: multi-range, garbage, wrong unit, inverted
		{"bytes=0-1,5-9", 0, 999, http.StatusOK},
		{"bytes=abc-", 0, 999, http.StatusOK},
		{"bytes=10-abc", 0, 999, http.StatusOK},
		{"bytes=200-100", 0, 999, http.StatusOK},
		{"items=0-10", 0, 999, http.StatusOK},
		{"bytes=", 0, 999, http.StatusOK},
		{"bytes=-", 0, 999, http.StatusOK},
	}
	for _, c := range cases {
		begin, end, status := parseRange(c.rng, size)
		if begin != c.begin || end != c.end || status != c.status {
			t.Errorf("parseRange(%q): got (%d, %d, %d) want (%d, %d, %d)",
				c.rng, begin, end, status, c.begin, c.end, c.status)
		}
	}
}
