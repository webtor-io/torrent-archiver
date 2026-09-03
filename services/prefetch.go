package services

import (
	"bytes"
	"context"
	"io"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/webtor-io/torrent-archiver/internal/fetch"
)

// Prefetcher reads upcoming small files while the writer is still emitting
// the current one. The archive stays a strictly sequential stream — zip and
// tar layouts are fixed — so the only way to overlap upstream latency is to
// have the next bodies already in memory when the writer asks for them.
//
// The plan is the file list in archive order. Every whole-file Fetch of file
// i starts the fetches of files i+1..i+depth that are small enough
// (maxFile) and fit the memory budget; a Fetch that finds its file already
// buffered returns the buffer, otherwise it falls through to the base
// fetcher. Partial (Range) fetches — the two files cut by the request's
// byte window — always go live. Big files are never prefetched: they are
// streamed as before, and torrent-http-proxy caps distinct big files per
// session at five, a budget the user's own player shares.
//
// Prefetches live and die with the request context: a client that
// disconnects cancels them (the archiver is stateless; keeping bodies past
// the client is torrent-web-seeder's job, see reader linger there). At the
// tail of a Range window up to depth small files are fetched for nothing;
// that waste is bounded by depth × maxFile.
type Prefetcher struct {
	base    fetch.Fetcher
	depth   int
	maxFile int64
	budget  int64

	index map[string]int // url → position in plan
	plan  []planned

	mu      sync.Mutex
	used    int64
	entries map[string]*prefetched
}

type planned struct {
	url  string
	size int64
}

type prefetched struct {
	size int64
	done chan struct{}
	buf  []byte
	err  error
}

// PrefetchConfig is what the flags provide; Depth == 0 disables prefetching.
type PrefetchConfig struct {
	Depth   int
	MaxFile int64
	Budget  int64
}

const (
	prefetchDepthFlag   = "prefetch-depth"
	prefetchMaxFileFlag = "prefetch-max-file-bytes"
	prefetchBudgetFlag  = "prefetch-budget-bytes"
)

func RegisterPrefetchFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.IntFlag{
			Name:   prefetchDepthFlag,
			Usage:  "how many upcoming small files to fetch ahead of the sequential archive stream (0 disables)",
			Value:  4,
			EnvVar: "PREFETCH_DEPTH",
		},
		cli.Int64Flag{
			Name:   prefetchMaxFileFlag,
			Usage:  "files larger than this are streamed, never prefetched",
			Value:  8 * 1024 * 1024,
			EnvVar: "PREFETCH_MAX_FILE_BYTES",
		},
		cli.Int64Flag{
			Name:   prefetchBudgetFlag,
			Usage:  "memory cap for prefetched bodies per archive request",
			Value:  64 * 1024 * 1024,
			EnvVar: "PREFETCH_BUDGET_BYTES",
		},
	)
}

func PrefetchConfigFromCLI(c *cli.Context) PrefetchConfig {
	return PrefetchConfig{
		Depth:   c.Int(prefetchDepthFlag),
		MaxFile: c.Int64(prefetchMaxFileFlag),
		Budget:  c.Int64(prefetchBudgetFlag),
	}
}

// NewPrefetcher wraps base for one archive request. plan is the request's
// files in archive order; a nil result means prefetching is off and the
// caller should keep the base fetcher.
func NewPrefetcher(base fetch.Fetcher, cfg PrefetchConfig, plan []planned) *Prefetcher {
	if cfg.Depth <= 0 || cfg.MaxFile <= 0 || cfg.Budget <= 0 || len(plan) < 2 {
		return nil
	}
	index := make(map[string]int, len(plan))
	for i, p := range plan {
		index[p.url] = i
	}
	return &Prefetcher{
		base:    base,
		depth:   cfg.Depth,
		maxFile: cfg.MaxFile,
		budget:  cfg.Budget,
		index:   index,
		plan:    plan,
		entries: map[string]*prefetched{},
	}
}

// planFor builds the prefetch plan from the archive's file list.
func planFor(files []file, baseURL, infoHash, suffix, token, apiKey string) []planned {
	out := make([]planned, 0, len(files))
	for _, f := range files {
		out = append(out, planned{url: fileURL(baseURL, infoHash, f.path, suffix, token, apiKey), size: int64(f.size)})
	}
	return out
}

// Fetch serves a whole-file read from the buffer when one was started for
// this url, and in every case kicks off the reads that should be in flight
// behind it. Partial reads and files outside the plan go straight upstream.
func (p *Prefetcher) Fetch(ctx context.Context, url string, begin, end int64) (io.ReadCloser, error) {
	i, planned := p.index[url]
	if planned {
		p.startAhead(ctx, i)
	}
	if !planned || !fetch.Whole(begin, end, p.plan[i].size) {
		return p.base.Fetch(ctx, url, begin, end)
	}
	p.mu.Lock()
	e := p.entries[url]
	p.mu.Unlock()
	if e == nil {
		return p.base.Fetch(ctx, url, begin, end)
	}
	select {
	case <-e.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e.err != nil {
		// The prefetch failed (upstream hiccup while the writer was busy
		// elsewhere); a live retry is the same request the writer would
		// have made without prefetching.
		p.release(url, e)
		return p.base.Fetch(ctx, url, begin, end)
	}
	return &bufferedBody{Reader: bytes.NewReader(e.buf), release: func() { p.release(url, e) }}, nil
}

// startAhead makes sure files i+1..i+depth that qualify are being fetched.
func (p *Prefetcher) startAhead(ctx context.Context, i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for j := i + 1; j <= i+p.depth && j < len(p.plan); j++ {
		f := p.plan[j]
		if f.size <= 0 || f.size > p.maxFile {
			continue
		}
		if _, started := p.entries[f.url]; started {
			continue
		}
		if p.used+f.size > p.budget {
			// Budget is a hard cap, not a queue: whatever does not fit now
			// is fetched live when the writer gets there.
			continue
		}
		p.used += f.size
		e := &prefetched{size: f.size, done: make(chan struct{})}
		p.entries[f.url] = e
		go p.run(ctx, f, e)
	}
}

func (p *Prefetcher) run(ctx context.Context, f planned, e *prefetched) {
	defer close(e.done)
	body, err := p.base.Fetch(ctx, f.url, 0, -1)
	if err != nil {
		e.err = err
		return
	}
	defer func() { _ = body.Close() }()
	buf := make([]byte, 0, f.size)
	// LimitReader guards the budget against an upstream that ignores the
	// declared size; a short body is detected by the length check below.
	buf, err = readAllInto(buf, io.LimitReader(body, f.size))
	if err != nil {
		e.err = err
		return
	}
	if int64(len(buf)) != f.size {
		e.err = io.ErrUnexpectedEOF
		log.WithField("url", redactURL(f.url)).WithField("want", f.size).WithField("got", len(buf)).Warn("prefetch: short body, will fetch live")
		return
	}
	e.buf = buf
}

// release returns the entry's memory to the budget once its body has been
// consumed or given up on. Idempotent per entry.
func (p *Prefetcher) release(url string, e *prefetched) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.entries[url]; ok && cur == e {
		delete(p.entries, url)
		p.used -= e.size
		e.buf = nil
	}
}

type bufferedBody struct {
	*bytes.Reader
	release func()
	once    sync.Once
}

func (b *bufferedBody) Close() error {
	b.once.Do(b.release)
	return nil
}

func readAllInto(buf []byte, r io.Reader) ([]byte, error) {
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return buf, err
		}
	}
}

// redactURL drops the query string — it carries the user's token.
func redactURL(u string) string {
	for i := 0; i < len(u); i++ {
		if u[i] == '?' {
			return u[:i]
		}
	}
	return u
}
