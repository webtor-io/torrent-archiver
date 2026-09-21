package services

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	"github.com/webtor-io/torrent-archiver/internal/fetch"
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

// fixedFiles is a fileSource that answers every info hash with the same
// list — the handler under test never needs the real torrent store.
type fixedFiles []file

func (f fixedFiles) Get(string) ([]file, error) { return f, nil }

// filesNamed builds the file list the upstream fixture can serve: the
// fixture derives content size from the name's trailing "-N".
func filesNamed(names ...string) fixedFiles {
	out := make(fixedFiles, 0, len(names))
	for _, n := range names {
		out = append(out, file{path: n, size: uint64(sizeOf(n)), modified: time.Unix(0, 0)})
	}
	return out
}

// newTestWeb wires a handler against an upstream URL. Prefetching is left
// off so every fetch is a live GET and the counters are easy to predict.
func newTestWeb(u *upstream, files fixedFiles) *Web {
	return &Web{
		ts:              files,
		cl:              u.srv.Client(),
		apiKey:          "k",
		apiSecret:       "s",
		torrentProxyUrl: u.srv.URL,
	}
}

func archiveRequest(ctx context.Context, name string) *http.Request {
	r := httptest.NewRequestWithContext(ctx, "GET", "/"+name, nil)
	r.Header.Set("X-Info-Hash", "deadbeef")
	return r
}

// counters snapshots every series a handler test asserts on, so tests
// compare deltas and stay independent of ordering on the shared registry.
type counters struct {
	archives  map[string]float64 // format|outcome
	bytes     map[string]float64 // format
	fetches   map[string]float64 // outcome
	durations map[string]uint64  // format → sample count
}

func snapshot(t *testing.T) counters {
	t.Helper()
	c := counters{map[string]float64{}, map[string]float64{}, map[string]float64{}, map[string]uint64{}}
	for _, f := range []string{formatZip, formatTar} {
		for _, o := range []string{outcomeOK, outcomeClientGone, outcomeUpstreamError, outcomeError} {
			c.archives[f+"|"+o] = testutil.ToFloat64(archivesTotal.WithLabelValues(f, o))
		}
		c.bytes[f] = testutil.ToFloat64(bytesTotal.WithLabelValues(f))
		c.durations[f] = histogramCount(t, archiveDuration.WithLabelValues(f))
	}
	for _, o := range []string{fetchOK, fetchCanceled, fetchError} {
		c.fetches[o] = testutil.ToFloat64(fileFetchesTotal.WithLabelValues(o))
	}
	return c
}

func histogramCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	m := &dto.Metric{}
	if err := o.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

func (c counters) archiveDelta(t *testing.T, format, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(archivesTotal.WithLabelValues(format, outcome)) - c.archives[format+"|"+outcome]
}

func (c counters) fetchDelta(t *testing.T, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(fileFetchesTotal.WithLabelValues(outcome)) - c.fetches[outcome]
}

// expectArchives asserts exactly one archive outcome moved, by exactly one.
func (c counters) expectArchives(t *testing.T, format, outcome string) {
	t.Helper()
	for _, f := range []string{formatZip, formatTar} {
		for _, o := range []string{outcomeOK, outcomeClientGone, outcomeUpstreamError, outcomeError} {
			want := 0.0
			if f == format && o == outcome {
				want = 1
			}
			if got := c.archiveDelta(t, f, o); got != want {
				t.Errorf("archives_total{%s,%s}: delta %v, want %v", f, o, got, want)
			}
		}
	}
	if got := histogramCount(t, archiveDuration.WithLabelValues(format)) - c.durations[format]; got != 1 {
		t.Errorf("archive_duration_seconds{%s}: %d new samples, want 1", format, got)
	}
}

func TestHandler_OKCountsArchiveBytesAndFetches(t *testing.T) {
	for _, format := range []string{formatZip, formatTar} {
		t.Run(format, func(t *testing.T) {
			u := newUpstream(0)
			defer u.srv.Close()
			web := newTestWeb(u, filesNamed("a-100", "b-50"))
			before := snapshot(t)

			rec := httptest.NewRecorder()
			web.ServeHTTP(rec, archiveRequest(context.Background(), "x."+format))

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			body := rec.Body.Bytes()
			if cl, _ := strconv.Atoi(rec.Header().Get("Content-Length")); cl != len(body) {
				t.Fatalf("Content-Length %d, body %d", cl, len(body))
			}
			before.expectArchives(t, format, outcomeOK)
			if got := testutil.ToFloat64(bytesTotal.WithLabelValues(format)) - before.bytes[format]; got != float64(len(body)) {
				t.Errorf("bytes_total{%s}: delta %v, want %d", format, got, len(body))
			}
			if got := before.fetchDelta(t, fetchOK); got != 2 {
				t.Errorf("file_fetches_total{ok}: delta %v, want 2", got)
			}
			if got := before.fetchDelta(t, fetchError) + before.fetchDelta(t, fetchCanceled); got != 0 {
				t.Errorf("non-ok fetches: %v, want 0", got)
			}
			if got := testutil.ToFloat64(archivesActive); got != 0 {
				t.Errorf("archives_active after request: %v, want 0", got)
			}
		})
	}
}

func TestHandler_UpstreamErrorIsNotClientGone(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	u := &upstream{srv: bad}
	web := newTestWeb(u, filesNamed("a-100"))
	before := snapshot(t)

	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, archiveRequest(context.Background(), "x.zip"))

	// Headers were already committed when the fetch failed, so the status
	// is 200 with a truncated body — the metric is the only honest signal.
	before.expectArchives(t, formatZip, outcomeUpstreamError)
	if got := before.fetchDelta(t, fetchError); got != 1 {
		t.Errorf("file_fetches_total{error}: delta %v, want 1", got)
	}
}

func TestHandler_ClientGoneIsNotAnError(t *testing.T) {
	// Upstream holds its response so the handler is blocked in a fetch
	// when the client leaves; the server then cancels the request context.
	u := newUpstream(2 * time.Second)
	defer u.srv.Close()
	web := newTestWeb(u, filesNamed("a-100"))
	srv := httptest.NewServer(web)
	defer srv.Close()
	before := snapshot(t)

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/x.zip", nil)
	req.Header.Set("X-Info-Hash", "deadbeef")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	// Headers are flushed before the body streams, so Do returns while the
	// handler is still waiting on upstream. Hanging up now is the
	// download-manager-gives-up scenario.
	cancel()
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()

	deadline := time.Now().Add(5 * time.Second)
	for before.archiveDelta(t, formatZip, outcomeClientGone) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	before.expectArchives(t, formatZip, outcomeClientGone)
	if got := before.fetchDelta(t, fetchCanceled); got != 1 {
		t.Errorf("file_fetches_total{canceled}: delta %v, want 1", got)
	}
}

type failingFiles struct{ err error }

func (f failingFiles) Get(string) ([]file, error) { return nil, f.err }

func TestHandler_StoreFailureIsError(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	web := newTestWeb(u, nil)
	web.ts = failingFiles{err: io.ErrUnexpectedEOF}
	before := snapshot(t)

	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, archiveRequest(context.Background(), "x.zip"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	before.expectArchives(t, formatZip, outcomeError)
	if got := testutil.ToFloat64(archivesActive); got != 0 {
		t.Errorf("archives_active after request: %v, want 0", got)
	}
}

// TestArchiveOutcome pins each disconnect signal separately: the server
// cancels the request context when the client drops, but a write that
// races that cancellation surfaces as EPIPE/ECONNRESET on a live context,
// and a fetch that lost the race surfaces as a wrapped context.Canceled.
func TestArchiveOutcome(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	wrap := func(err error) error { return errors.Wrap(err, "failed to write x") }
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"nil", live, nil, outcomeOK},
		{"ctx canceled, opaque error", canceled, errors.New("write: something"), outcomeClientGone},
		{"live ctx, wrapped context.Canceled", live, wrap(context.Canceled), outcomeClientGone},
		{"live ctx, broken pipe", live, wrap(&net.OpError{Op: "write", Err: syscall.EPIPE}), outcomeClientGone},
		{"live ctx, connection reset", live, wrap(&net.OpError{Op: "write", Err: syscall.ECONNRESET}), outcomeClientGone},
		{"live ctx, upstream", live, wrap(&fetch.Error{Err: errors.New("502")}), outcomeUpstreamError},
		{"live ctx, other", live, wrap(io.ErrUnexpectedEOF), outcomeError},
	}
	for _, c := range cases {
		if got := archiveOutcome(c.ctx, c.err); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestHandler_TruncatedUpstreamBodyIsUpstreamError(t *testing.T) {
	// Headers say 100 bytes, the body carries 50: the failure arrives from
	// the body read, not from the GET, and must still be upstream's.
	short := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Length", "100")
		_, _ = rw.Write(make([]byte, 50))
	}))
	defer short.Close()
	u := &upstream{srv: short}
	web := newTestWeb(u, filesNamed("a-100"))
	before := snapshot(t)

	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, archiveRequest(context.Background(), "x.tar"))

	before.expectArchives(t, formatTar, outcomeUpstreamError)
	// The GET itself succeeded; only the body was short.
	if got := before.fetchDelta(t, fetchOK); got != 1 {
		t.Errorf("file_fetches_total{ok}: delta %v, want 1", got)
	}
}
