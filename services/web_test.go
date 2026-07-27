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

func TestParseSelectedPaths(t *testing.T) {
	cases := []struct {
		in  []string
		out []string
		ok  bool
	}{
		{nil, nil, true},
		{[]string{"/Torrent/dir/file.mkv"}, []string{"Torrent/dir/file.mkv"}, true},
		{[]string{"Torrent/dir/"}, []string{"Torrent/dir"}, true},
		{[]string{"", "/"}, nil, true},
		{[]string{"a", "b"}, []string{"a", "b"}, true},
		// canonical order + dedup: selection is a set
		{[]string{"b", "a", "b"}, []string{"a", "b"}, true},
	}
	for _, c := range cases {
		out, ok := parseSelectedPaths(c.in)
		if ok != c.ok || len(out) != len(c.out) {
			t.Fatalf("parseSelectedPaths(%v): got (%v, %v) want (%v, %v)", c.in, out, ok, c.out, c.ok)
		}
		for i := range out {
			if out[i] != c.out[i] {
				t.Errorf("parseSelectedPaths(%v)[%d]: got %q want %q", c.in, i, out[i], c.out[i])
			}
		}
	}
	many := make([]string, maxSelectedPaths+1)
	if _, ok := parseSelectedPaths(many); ok {
		t.Error("expected over-limit selection to be rejected")
	}
	if _, ok := parseSelectedPaths([]string{"a\x00b"}); ok {
		t.Error("expected NUL-carrying path to be rejected")
	}
}

func TestMatchesAny(t *testing.T) {
	sel := []string{"T/Season1", "T/extras/sample.mkv"}
	prefixes := []string{"T/Season1/", "T/extras/sample.mkv/"}
	cases := []struct {
		path string
		want bool
	}{
		{"T/Season1/e01.mkv", true},
		{"T/Season1/sub/e02.mkv", true},
		{"T/Season1", true},
		{"T/Season10/e01.mkv", false}, // prefix must respect path boundary
		{"T/extras/sample.mkv", true},
		{"T/extras/sample.mkv.srt", false},
		{"T/other.mkv", false},
	}
	for _, c := range cases {
		if got := matchesAny(c.path, sel, prefixes); got != c.want {
			t.Errorf("matchesAny(%q): got %v want %v", c.path, got, c.want)
		}
	}
}
