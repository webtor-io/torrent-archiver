package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webtor-io/torrent-archiver/internal/fetch"
	"github.com/webtor-io/torrent-archiver/tarhttp"
)

// upstream serves /<name> as bytes.Repeat(name[0], size(name)) where the
// size is the number after the dash: "a-1000" is 1000 'a's. It records
// concurrency and per-path hits, and can be told to hold every response for
// a while so overlap becomes observable.
type upstream struct {
	srv      *httptest.Server
	inflight atomic.Int32
	maxSeen  atomic.Int32
	hits     sync.Map // path → *atomic.Int32
	hold     time.Duration
	short    map[string]bool // paths that return fewer bytes than declared
}

func sizeOf(name string) int {
	n, _ := strconv.Atoi(name[strings.LastIndex(name, "-")+1:])
	return n
}

func newUpstream(hold time.Duration) *upstream {
	u := &upstream{hold: hold, short: map[string]bool{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		cur := u.inflight.Add(1)
		defer u.inflight.Add(-1)
		for {
			m := u.maxSeen.Load()
			if cur <= m || u.maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		name := strings.TrimPrefix(req.URL.Path, "/")
		c, _ := u.hits.LoadOrStore(name, new(atomic.Int32))
		c.(*atomic.Int32).Add(1)
		if u.hold > 0 {
			time.Sleep(u.hold)
		}
		data := bytes.Repeat([]byte{name[0]}, sizeOf(name))
		if u.short[name] {
			data = data[:len(data)/2]
		}
		if rng := req.Header.Get("Range"); rng != "" {
			parts := strings.Split(strings.TrimPrefix(rng, "bytes="), "-")
			b, _ := strconv.Atoi(parts[0])
			e, _ := strconv.Atoi(parts[1])
			rw.WriteHeader(http.StatusPartialContent)
			_, _ = rw.Write(data[b : e+1])
			return
		}
		_, _ = rw.Write(data)
	}))
	return u
}

func (u *upstream) hitCount(name string) int32 {
	c, ok := u.hits.Load(name)
	if !ok {
		return 0
	}
	return c.(*atomic.Int32).Load()
}

func (u *upstream) plan(names ...string) []planned {
	out := make([]planned, 0, len(names))
	for _, n := range names {
		out = append(out, planned{url: u.srv.URL + "/" + n, size: int64(sizeOf(n))})
	}
	return out
}

func fetchAll(t *testing.T, f fetch.Fetcher, ctx context.Context, url string, begin, end int64) []byte {
	t.Helper()
	rc, err := f.Fetch(ctx, url, begin, end)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

func TestPrefetcher_ServesBufferedBodiesInOrder(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	plan := u.plan("a-100", "b-200", "c-300", "d-400", "e-500")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 2, MaxFile: 1 << 20, Budget: 1 << 20}, plan)
	ctx := context.Background()
	for i, f := range plan {
		got := fetchAll(t, p, ctx, f.url, 0, -1)
		if len(got) != int(f.size) || got[0] != byte('a'+i) {
			t.Fatalf("file %d: got %d bytes of %q, want %d of %q", i, len(got), got[:1], f.size, 'a'+i)
		}
	}
	// Every file was fetched from upstream exactly once: the buffered body
	// was used, not a second live request.
	for _, n := range []string{"a-100", "b-200", "c-300", "d-400", "e-500"} {
		if c := u.hitCount(n); c != 1 {
			t.Errorf("%s fetched %d times, want 1", n, c)
		}
	}
	if p.used != 0 {
		t.Errorf("budget not returned: used=%d", p.used)
	}
}

func TestPrefetcher_OverlapsUpstreamLatency(t *testing.T) {
	u := newUpstream(150 * time.Millisecond)
	defer u.srv.Close()
	plan := u.plan("a-10", "b-10", "c-10", "d-10", "e-10", "f-10")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 3, MaxFile: 1 << 20, Budget: 1 << 20}, plan)
	start := time.Now()
	for _, f := range plan {
		fetchAll(t, p, context.Background(), f.url, 0, -1)
	}
	sequential := time.Duration(len(plan)) * 150 * time.Millisecond
	if el := time.Since(start); el > sequential*2/3 {
		t.Errorf("took %v, sequential would be %v — no overlap happened", el, sequential)
	}
	if m := u.maxSeen.Load(); m < 2 || m > 4 {
		t.Errorf("max concurrent upstream requests %d, want 2..4 (depth 3 + current)", m)
	}
}

func TestPrefetcher_BigAndPartialGoLive(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	plan := u.plan("a-100", "b-5000", "c-100")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 4, MaxFile: 1000, Budget: 1 << 20}, plan)
	ctx := context.Background()
	fetchAll(t, p, ctx, plan[0].url, 0, -1)
	// b is over MaxFile: never buffered, fetched live exactly when asked.
	if c := u.hitCount("b-5000"); c != 0 {
		t.Fatalf("big file prefetched (%d hits) — must stream live", c)
	}
	got := fetchAll(t, p, ctx, plan[1].url, 10, 20)
	if string(got) != strings.Repeat("b", 10) {
		t.Fatalf("partial body wrong: %q", got)
	}
	// c was prefetched while a/b were served; a partial read of it must
	// still go live (the buffer only answers whole-file reads).
	got = fetchAll(t, p, ctx, plan[2].url, 0, 50)
	if len(got) != 50 {
		t.Fatalf("partial c: %d bytes", len(got))
	}
	if c := u.hitCount("c-100"); c != 2 {
		t.Errorf("c fetched %d times, want 2 (one prefetch, one live partial)", c)
	}
}

func TestPrefetcher_BudgetIsAHardCap(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	plan := u.plan("a-100", "b-600", "c-600", "d-100")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 4, MaxFile: 1000, Budget: 700}, plan)
	p.startAhead(context.Background(), 0)
	// b (600) fits, c (600) would exceed 700, d (100) fits alongside b.
	p.mu.Lock()
	_, hasB := p.entries[plan[1].url]
	_, hasC := p.entries[plan[2].url]
	_, hasD := p.entries[plan[3].url]
	used := p.used
	p.mu.Unlock()
	if !hasB || hasC || !hasD || used != 700 {
		t.Fatalf("budget: b=%v c=%v d=%v used=%d, want b,d in flight and used=700", hasB, hasC, hasD, used)
	}
}

func TestPrefetcher_ShortBodyFallsBackToLive(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	u.short["b-100"] = true
	plan := u.plan("a-10", "b-100")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 1, MaxFile: 1000, Budget: 1000}, plan)
	ctx := context.Background()
	fetchAll(t, p, ctx, plan[0].url, 0, -1)
	got := fetchAll(t, p, ctx, plan[1].url, 0, -1)
	// The live retry hits the same short upstream, so the body is short
	// again — what matters is that the buffer was not trusted and the
	// request went upstream a second time, where a real seeder would answer
	// in full.
	if c := u.hitCount("b-100"); c != 2 {
		t.Errorf("b fetched %d times, want 2 (short prefetch discarded, then live)", c)
	}
	if len(got) != 50 {
		t.Errorf("live body length %d", len(got))
	}
}

func TestPrefetcher_CancelStopsPrefetches(t *testing.T) {
	u := newUpstream(300 * time.Millisecond)
	defer u.srv.Close()
	plan := u.plan("a-10", "b-10", "c-10")
	p := NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, PrefetchConfig{Depth: 2, MaxFile: 1000, Budget: 1000}, plan)
	ctx, cancel := context.WithCancel(context.Background())
	p.startAhead(ctx, 0)
	cancel()
	p.mu.Lock()
	e := p.entries[plan[1].url]
	p.mu.Unlock()
	select {
	case <-e.done:
	case <-time.After(2 * time.Second):
		t.Fatal("prefetch did not stop on cancel")
	}
	if e.err == nil {
		t.Error("cancelled prefetch must record an error, not a body")
	}
}

// The archive bytes must not depend on prefetching: same tar with depth 0
// and depth 3, byte for byte, including a Range window that cuts files.
func TestPrefetcher_TarBytesIdentical(t *testing.T) {
	u := newUpstream(0)
	defer u.srv.Close()
	names := []string{"a-700", "b-40", "c-9000", "d-3", "e-1200"}
	plan := u.plan(names...)
	build := func(pf *Prefetcher, begin, end int64) []byte {
		var buf bytes.Buffer
		w := tarhttp.NewWriter(&buf, begin, end, u.srv.Client())
		if pf != nil {
			w.SetFetcher(pf)
		}
		for i, n := range names {
			fh := &tarhttp.FileHeader{Name: n, URL: plan[i].url, Size: uint64(sizeOf(n)), Modified: time.Unix(1000, 0)}
			if err := w.CreateHeader(context.Background(), fh); err != nil {
				t.Fatalf("create %s: %v", n, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	cfg := PrefetchConfig{Depth: 3, MaxFile: 1000, Budget: 1 << 20}
	whole := build(nil, 0, -1)
	if got := build(NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, cfg, plan), 0, -1); !bytes.Equal(got, whole) {
		t.Fatal("prefetched whole archive differs from sequential")
	}
	for _, win := range [][2]int64{{0, 511}, {600, 1300}, {1300, 9999}, {5000, -1}} {
		want := build(nil, win[0], win[1])
		got := build(NewPrefetcher(fetch.HTTP{Client: u.srv.Client()}, cfg, plan), win[0], win[1])
		if !bytes.Equal(got, want) {
			t.Fatalf("window %v: prefetched bytes differ from sequential", win)
		}
	}
}
