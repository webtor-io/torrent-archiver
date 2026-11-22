package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
		refusals:        newRefusals(refusalTTL, refusalCap),
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

// HEAD must carry exactly the headers GET does — download managers probe
// with HEAD and then resume with Range against its Content-Length — and
// must not read a single file body. A real server is used, not a recorder:
// net/http silently eats body writes on HEAD, which is what hid the bug.
func TestHandler_HeadMatchesGetWithoutReadingFiles(t *testing.T) {
	for _, format := range []string{formatZip, formatTar} {
		for _, rng := range []string{"", "bytes=10-600", "bytes=100-", "bytes=99999-"} {
			t.Run(format+" "+rng, func(t *testing.T) {
				u := newUpstream(0)
				defer u.srv.Close()
				web := newTestWeb(u, filesNamed("a-100", "b-50"))
				do := func(method string) (*http.Response, []byte) {
					// The handler may go on after the client has its
					// headers. Wait for it to return before srv.Close drops
					// the connection: that cancels its context, and a HEAD
					// that builds the archive would stop after however many
					// fetches it got to by then.
					done := make(chan struct{})
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(done)
						web.ServeHTTP(w, r)
					}))
					defer srv.Close()
					req, _ := http.NewRequest(method, srv.URL+"/x."+format, nil)
					req.Header.Set("X-Info-Hash", "deadbeef")
					if rng != "" {
						req.Header.Set("Range", rng)
					}
					res, err := srv.Client().Do(req)
					if err != nil {
						t.Fatalf("%s: %v", method, err)
					}
					body, _ := io.ReadAll(res.Body)
					_ = res.Body.Close()
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Fatalf("%s: handler still running after 10 s", method)
					}
					return res, body
				}

				get, body := do(http.MethodGet)
				getHits := u.totalHits()
				if get.StatusCode != http.StatusRequestedRangeNotSatisfiable {
					if cl, _ := strconv.Atoi(get.Header.Get("Content-Length")); cl != len(body) {
						t.Fatalf("GET Content-Length %d, body %d", cl, len(body))
					}
					if getHits == 0 {
						t.Fatal("GET read nothing upstream: the fixture is not exercised")
					}
				}

				before := snapshot(t)
				head, _ := do(http.MethodHead)
				// Primary: a HEAD records no archive outcome.
				for _, f := range []string{formatZip, formatTar} {
					for _, o := range []string{outcomeOK, outcomeClientGone, outcomeUpstreamError, outcomeError} {
						if got := before.archiveDelta(t, f, o); got != 0 {
							t.Errorf("HEAD moved archives_total{%s,%s} by %v", f, o, got)
						}
					}
				}
				if got := u.totalHits() - getHits; got != 0 {
					t.Errorf("HEAD made %d upstream requests, want 0", got)
				}
				if head.StatusCode != get.StatusCode {
					t.Errorf("HEAD status %d, GET %d", head.StatusCode, get.StatusCode)
				}
				// net/http adds Content-Length: 0 to the empty GET 416 on its
				// own and omits it on HEAD; that body length is not the archive's.
				skip := map[string]bool{"Date": true}
				if get.StatusCode == http.StatusRequestedRangeNotSatisfiable {
					skip["Content-Length"] = true
				}
				for _, h := range []http.Header{get.Header, head.Header} {
					for k := range h {
						if !skip[k] && get.Header.Get(k) != head.Header.Get(k) {
							t.Errorf("%s: GET %q, HEAD %q", k, get.Header.Get(k), head.Header.Get(k))
						}
					}
				}
				if got := testutil.ToFloat64(archivesActive); got != 0 {
					t.Errorf("archives_active after HEAD: %v, want 0", got)
				}
			})
		}
	}
}

func (u *upstream) totalHits() (n int32) {
	u.hits.Range(func(_, c any) bool {
		n += c.(*atomic.Int32).Load()
		return true
	})
	return n
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

// Prefetching must engage through the handler — the URLs the writers fetch
// are the ones the prefetcher locates — and must not change a byte of
// either format, whole or in a Range window.
func TestHandler_PrefetchEngagesAndKeepsBytes(t *testing.T) {
	names := []string{"a-700", "b-40", "c-9000", "d-3", "e-1200", "f-300"}
	on := PrefetchConfig{Depth: 3, MaxFile: 2000, Budget: 1 << 20}
	for _, format := range []string{formatZip, formatTar} {
		t.Run(format, func(t *testing.T) {
			u := newUpstream(20 * time.Millisecond)
			defer u.srv.Close()
			get := func(cfg PrefetchConfig, rng string) []byte {
				web := newTestWeb(u, filesNamed(names...))
				web.prefetch = cfg
				r := archiveRequest(context.Background(), "x."+format)
				if rng != "" {
					r.Header.Set("Range", rng)
				}
				rec := httptest.NewRecorder()
				web.ServeHTTP(rec, r)
				return rec.Body.Bytes()
			}
			for _, rng := range []string{"", "bytes=600-1900", "bytes=5000-"} {
				want := get(PrefetchConfig{}, rng)
				u.maxSeen.Store(0)
				if got := get(on, rng); !bytes.Equal(got, want) {
					t.Fatalf("range %q: prefetched archive differs (%d vs %d bytes)", rng, len(got), len(want))
				}
				if rng == "" && u.maxSeen.Load() < 2 {
					t.Errorf("prefetch never engaged: at most %d upstream request at a time", u.maxSeen.Load())
				}
			}
		})
	}
}

// statusUpstream answers every request with status and no body, counting
// requests by method and logging each as "METHOD path range"; HEADs wait
// headHold first.
type statusUpstream struct {
	srv         *httptest.Server
	status      atomic.Int32
	heads, gets atomic.Int32
	mu          sync.Mutex
	reqs        []string
}

func newStatusUpstream(status int, headHold time.Duration) *statusUpstream {
	u := &statusUpstream{}
	u.status.Store(int32(status))
	u.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r.Method+" "+r.URL.Path+" "+r.Header.Get("Range"))
		u.mu.Unlock()
		if r.Method == http.MethodHead {
			u.heads.Add(1)
			time.Sleep(headHold)
		} else {
			u.gets.Add(1)
		}
		rw.WriteHeader(int(u.status.Load()))
	}))
	return u
}

func (u *statusUpstream) web(files fixedFiles) *Web {
	return newTestWeb(&upstream{srv: u.srv}, files)
}

// serve runs one request through the handler; token "" keeps the
// handler's own signed token.
func serve(web *Web, method, name, rng, token string) *httptest.ResponseRecorder {
	r := archiveRequest(context.Background(), name)
	r.Method = method
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	if token != "" {
		r.Header.Set("X-Token", token)
	}
	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, r)
	return rec
}

func refusedCount(code int) float64 {
	return testutil.ToFloat64(archivesRefused.WithLabelValues(strconv.Itoa(code)))
}

// A torrent upstream refuses is refused with upstream's status before a
// single archive byte or header: the incident was 206 + the tar header,
// then a failure on the first file, then the download manager's retry.
func TestHandler_RefusedBeforeFirstByte(t *testing.T) {
	for _, format := range []string{formatZip, formatTar} {
		for _, code := range refusedStatuses {
			t.Run(format+" "+strconv.Itoa(code), func(t *testing.T) {
				u := newStatusUpstream(code, 0)
				defer u.srv.Close()
				before := snapshot(t)
				refused := refusedCount(code)

				rec := serve(u.web(filesNamed("T/a-100", "T/b-50")), http.MethodGet, "x."+format, "bytes=0-", "")

				if rec.Code != code {
					t.Fatalf("status %d, want %d", rec.Code, code)
				}
				if got := rec.Body.String(); got != http.StatusText(code)+"\n" {
					t.Errorf("body %q, want the plain status text", got)
				}
				for _, h := range []string{"Content-Disposition", "Content-Range", "Accept-Ranges", "Etag"} {
					if v := rec.Header().Get(h); v != "" {
						t.Errorf("refusal carries the archive's %s: %q", h, v)
					}
				}
				if h, g := u.heads.Load(), u.gets.Load(); h != 1 || g != 0 {
					t.Errorf("upstream got %d HEAD and %d GET, want one probe and nothing else", h, g)
				}
				if got := refusedCount(code) - refused; got != 1 {
					t.Errorf("archives_refused_total{%d}: delta %v, want 1", code, got)
				}
				before.expectNoArchive(t)
			})
		}
	}
}

// A HEAD asks upstream nothing: probing it would make the seeder load the
// torrent for a client that may never GET. It is refused from an answer a
// GET's probe left in memory, and reads no file either way.
func TestHandler_HeadAnsweredFromMemoryOnly(t *testing.T) {
	u := newStatusUpstream(http.StatusForbidden, 0)
	defer u.srv.Close()
	web := u.web(filesNamed("T/a-100"))
	for i, c := range []struct {
		method string
		want   int
		heads  int32
	}{
		{http.MethodHead, http.StatusOK, 0},
		{http.MethodGet, http.StatusForbidden, 1},
		{http.MethodHead, http.StatusForbidden, 1},
	} {
		if rec := serve(web, c.method, "x.tar", "", ""); rec.Code != c.want {
			t.Errorf("#%d %s: status %d, want %d", i, c.method, rec.Code, c.want)
		}
		if h, g := u.heads.Load(), u.gets.Load(); h != c.heads || g != 0 {
			t.Errorf("#%d %s: upstream got %d HEAD and %d GET, want %d and 0", i, c.method, h, g, c.heads)
		}
	}
}

// Retries are answered from memory, per token: upstream decides on it.
func TestHandler_RefusalAnsweredFromMemory(t *testing.T) {
	u := newStatusUpstream(http.StatusForbidden, 0)
	defer u.srv.Close()
	web := u.web(filesNamed("T/a-100", "T/b-50"))
	refused := refusedCount(http.StatusForbidden)

	for _, c := range []struct{ method, rng string }{
		{http.MethodGet, ""}, {http.MethodGet, "bytes=0-"}, {http.MethodGet, "bytes=10-"}, {http.MethodHead, ""},
	} {
		if rec := serve(web, c.method, "x.zip", c.rng, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %q: status %d", c.method, c.rng, rec.Code)
		}
	}
	if got := u.heads.Load(); got != 1 {
		t.Errorf("4 requests for one read made %d probes, want 1", got)
	}
	if rec := serve(web, http.MethodGet, "x.zip", "", "another-user"); rec.Code != http.StatusForbidden {
		t.Fatalf("other token: status %d", rec.Code)
	}
	if got := u.heads.Load(); got != 2 {
		t.Errorf("another token was answered from memory: %d probes, want 2", got)
	}
	if got := refusedCount(http.StatusForbidden) - refused; got != 5 {
		t.Errorf("archives_refused_total{403}: delta %v, want 5 (memory answers count)", got)
	}
}

func TestHandler_RefusalExpires(t *testing.T) {
	u := newStatusUpstream(http.StatusForbidden, 0)
	defer u.srv.Close()
	web := u.web(filesNamed("T/a-100"))
	web.refusals = newRefusals(50*time.Millisecond, refusalCap)

	serve(web, http.MethodGet, "x.zip", "", "")
	serve(web, http.MethodGet, "x.zip", "", "")
	if got := u.heads.Load(); got != 1 {
		t.Fatalf("within the TTL: %d probes, want 1", got)
	}
	time.Sleep(150 * time.Millisecond)
	u.status.Store(http.StatusOK) // upstream serves the torrent again
	if rec := serve(web, http.MethodGet, "x.zip", "", ""); rec.Code != http.StatusOK {
		t.Errorf("after the TTL: status %d, want the archive (200)", rec.Code)
	}
	if got := u.heads.Load(); got != 2 {
		t.Errorf("after the TTL: %d probes, want 2", got)
	}
}

// A probe that fails any other way neither refuses nor is remembered: the
// archive starts as it did before the probe existed.
func TestHandler_ProbeFailureKeepsTodaysBehaviour(t *testing.T) {
	t.Run("5xx", func(t *testing.T) {
		u := newStatusUpstream(http.StatusBadGateway, 0)
		defer u.srv.Close()
		web := u.web(filesNamed("T/a-100"))
		refused := refusedCount(http.StatusForbidden) + refusedCount(http.StatusNotFound) + refusedCount(http.StatusUnavailableForLegalReasons)
		for i := 0; i < 2; i++ {
			before := snapshot(t)
			if rec := serve(web, http.MethodGet, "x.zip", "", ""); rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (headers committed, body cut)", rec.Code)
			}
			before.expectArchives(t, formatZip, outcomeUpstreamError)
		}
		if got := u.heads.Load(); got != 2 {
			t.Errorf("%d probes for 2 requests: a 5xx was remembered", got)
		}
		if got := refusedCount(http.StatusForbidden) + refusedCount(http.StatusNotFound) + refusedCount(http.StatusUnavailableForLegalReasons) - refused; got != 0 {
			t.Errorf("archives_refused_total moved by %v on a 5xx", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		defer func(d time.Duration) { probeTimeout = d }(probeTimeout)
		probeTimeout = 50 * time.Millisecond
		// Upstream would refuse, but only after the timeout.
		u := newStatusUpstream(http.StatusForbidden, time.Second)
		defer u.srv.Close()
		web := u.web(filesNamed("T/a-100"))
		started := time.Now()
		if rec := serve(web, http.MethodGet, "x.zip", "bytes=0-", ""); rec.Code != http.StatusPartialContent {
			t.Fatalf("status %d, want 206: a slow probe must not hold the archive", rec.Code)
		}
		if d := time.Since(started); d > 900*time.Millisecond {
			t.Errorf("waited %v for the probe", d)
		}
		serve(web, http.MethodGet, "x.zip", "bytes=0-", "")
		if got := u.heads.Load(); got != 2 {
			t.Errorf("%d probes for 2 requests: a timeout was remembered", got)
		}
	})
}

// The probe asks about the file the window reads first, not the first file
// of the torrent, with the Range of that file's content GET; a window that
// reads no content is not probed. Into the 2nd file, zip without a CRC
// checkpoint first GETs the gap from the file's byte 0: the probe must not
// ask for it, since with a checkpoint at the window's start Write never does.
func TestHandler_ProbesTheFileTheWindowReads(t *testing.T) {
	cases := []struct{ rng, want string }{
		{"", "/deadbeef/T/a-100"},
		{"bytes=3000-3001", "/deadbeef/T/b-9000"},
		{"bytes=-20", ""}, // zip end of central directory / tar end marker
	}
	for _, format := range []string{formatZip, formatTar} {
		for _, c := range cases {
			u := newStatusUpstream(http.StatusOK, 0)
			serve(u.web(filesNamed("T/a-100", "T/b-9000")), http.MethodGet, "x."+format, c.rng, "")
			u.srv.Close()
			var heads []string
			lastGet := map[string]string{} // path -> its last GET, the content read
			for _, r := range u.reqs {
				method, rest, _ := strings.Cut(r, " ")
				path, _, _ := strings.Cut(rest, " ")
				if method == http.MethodHead {
					heads = append(heads, r)
				} else {
					lastGet[path] = rest
				}
			}
			var want []string
			if c.want != "" {
				want = []string{http.MethodHead + " " + lastGet[c.want]}
			}
			if !slices.Equal(heads, want) {
				t.Errorf("%s %q: probed %q, want %q (upstream saw %q)", format, c.rng, heads, want, u.reqs)
			}
		}
	}
}

func TestHandler_RefusalMemoryIsBounded(t *testing.T) {
	u := newStatusUpstream(http.StatusForbidden, 0)
	defer u.srv.Close()
	web := u.web(filesNamed("T/a-100"))
	web.refusals = newRefusals(time.Minute, 10)
	for i := 0; i < 50; i++ {
		serve(web, http.MethodGet, "x.zip", "", "token-"+strconv.Itoa(i))
	}
	if n := web.refusals.Len(); n > 10 {
		t.Errorf("%d refusals kept, cap 10", n)
	}
}

// Archives upstream serves are byte-for-byte what they were before the
// probe: these digests were produced by the handler before it existed.
func TestHandler_OpenArchiveBytesUnchanged(t *testing.T) {
	names := []string{"T/a-700", "T/d/b-40", "T/d/c-9000", "T/e-0", "T/f-1200", "T/g/h-300"}
	cases := []struct {
		format, rng string
		status      int
		sha256      string
	}{
		{formatZip, "", 200, "5738186b49745c1a6f6eacd2ec7304015b6959bb1f7e995ab3fbccdb9d34ba97"},
		{formatZip, "bytes=0-0", 206, "5c62e091b8c0565f1bafad0dad5934276143ae2ccef7a5381e8ada5b1a8d26d2"},
		{formatZip, "bytes=600-1900", 206, "fc0384638b2325b09320cc3518a316bc0ac8b7fc2f425b3d51dcda4e6a921320"},
		{formatZip, "bytes=5000-", 206, "0f89234d53a274225914c37abf3a2a690c5b8b5c7a41221030b814981f70dca5"},
		{formatZip, "bytes=-100", 206, "9e3f351d1adcbfc37fdb7068a01546e4ff4a6f9dab684b08baa0c3849f2366b0"},
		{formatZip, "bytes=10000-10500", 206, "25fd206742ca85e417f0862ec3bcb9fbf427dfbdb69771cf68f47cf635c942b0"},
		{formatTar, "", 200, "f373fad097c351ba035defc77330cb7db7be67b85d67b84562df8351628c7fa6"},
		{formatTar, "bytes=0-0", 206, "e632b7095b0bf32c260fa4c539e9fd7b852d0de454e9be26f24d0d6f91d069d3"},
		{formatTar, "bytes=600-1900", 206, "d06442345e8cdfa7a148197b8437fd3b716d554f65ae109b3536543b771699ab"},
		{formatTar, "bytes=5000-", 206, "9047a948417ccd365f2d4f6493a5b599d0b2bdc1a5a8e66f235ced335659e7a5"},
		{formatTar, "bytes=-100", 206, "cd00e292c5970d3c5e2f0ffa5171e555bc46bfc4faddfb4a418b6840b86e79a3"},
		{formatTar, "bytes=10000-10500", 206, "ab33d36f9367030f0a835bcd92e976231ca9f3a1d2d69c678fc0f828e2dc5539"},
	}
	for _, c := range cases {
		u := newUpstream(0)
		rec := serve(newTestWeb(u, filesNamed(names...)), http.MethodGet, "x."+c.format, c.rng, "")
		u.srv.Close()
		if got := fmt.Sprintf("%x", sha256.Sum256(rec.Body.Bytes())); rec.Code != c.status || got != c.sha256 {
			t.Errorf("%s %q: status %d sha256 %s, want %d %s", c.format, c.rng, rec.Code, got, c.status, c.sha256)
		}
	}
}

// expectNoArchive asserts no archive outcome or duration was recorded.
func (c counters) expectNoArchive(t *testing.T) {
	t.Helper()
	for _, f := range []string{formatZip, formatTar} {
		for _, o := range []string{outcomeOK, outcomeClientGone, outcomeUpstreamError, outcomeError} {
			if got := c.archiveDelta(t, f, o); got != 0 {
				t.Errorf("archives_total{%s,%s}: delta %v, want 0", f, o, got)
			}
		}
		if got := histogramCount(t, archiveDuration.WithLabelValues(f)) - c.durations[f]; got != 0 {
			t.Errorf("archive_duration_seconds{%s}: %d new samples, want 0", f, got)
		}
	}
}
