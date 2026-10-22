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
// The plan is the request's file list in archive order, shared with the
// writer, not copied. Every whole-file Fetch of file i starts the fetches
// of files i+1..i+depth that are small enough (maxFile) and fit the memory
// budget; a Fetch that finds its file already buffered returns the buffer,
// otherwise it falls through to the base fetcher. Partial (Range) fetches —
// the two files cut by the request's byte window — always go live. Big
// files are never prefetched: they are streamed as before, and
// torrent-http-proxy caps distinct big files per session at five, a budget
// the user's own player shares.
//
// Prefetches live and die with the request context: a client that
// disconnects cancels them (the archiver is stateless; keeping bodies past
// the client is torrent-web-seeder's job, see reader linger there). At the
// tail of a Range window up to depth small files are fetched for nothing;
// that waste is bounded by depth × maxFile.
//
// URLs are built only for the files actually fetched, never for the whole
// plan: each carries the user's token, so a plan of URLs costs hundreds of
// bytes per file — tens of MiB per request of a 100k-file torrent, and
// concurrent requests of one such torrent pushed a replica past its 4 GiB
// limit.
type Prefetcher struct {
	base    fetch.Fetcher
	depth   int
	maxFile int64
	budget  int64

	files []file
	url   func(path string) string

	// next and nextURL are the writer's position in files, touched only by
	// the writer's own Fetch calls.
	next    int
	nextURL string

	mu      sync.Mutex
	used    int64
	entries map[int]*prefetched // position in files → body
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

// NewPrefetcher wraps base for one archive request. files is the request's
// file list in archive order and url maps a file's path to the URL the
// writer fetches it from; a nil result means prefetching is off and the
// caller should keep the base fetcher.
func NewPrefetcher(base fetch.Fetcher, cfg PrefetchConfig, files []file, url func(path string) string) *Prefetcher {
	if cfg.Depth <= 0 || cfg.MaxFile <= 0 || cfg.Budget <= 0 || len(files) < 2 {
		return nil
	}
	return &Prefetcher{
		base:    base,
		depth:   cfg.Depth,
		maxFile: cfg.MaxFile,
		budget:  cfg.Budget,
		files:   files,
		url:     url,
		entries: map[int]*prefetched{},
	}
}

// locate finds url's position in files. Both writers fetch in archive
// order, so the scan resumes where the previous Fetch stopped and costs one
// URL build per file over the whole request. A URL the scan does not find
// ends prefetching for the rest of the request rather than rescanning on
// every later Fetch.
func (p *Prefetcher) locate(url string) (int, bool) {
	for ; p.next < len(p.files); p.next++ {
		if p.nextURL == "" {
			p.nextURL = p.url(p.files[p.next].path)
		}
		if p.nextURL == url {
			return p.next, true
		}
		p.nextURL = ""
	}
	return 0, false
}

// Fetch serves a whole-file read from the buffer when one was started for
// this url, and in every case kicks off the reads that should be in flight
// behind it. Partial reads and files outside the plan go straight upstream.
func (p *Prefetcher) Fetch(ctx context.Context, url string, begin, end int64) (io.ReadCloser, error) {
	i, planned := p.locate(url)
	if planned {
		p.startAhead(ctx, i)
	}
	if !planned || !fetch.Whole(begin, end, int64(p.files[i].size)) {
		return p.base.Fetch(ctx, url, begin, end)
	}
	p.mu.Lock()
	e := p.entries[i]
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
		p.release(i, e)
		return p.base.Fetch(ctx, url, begin, end)
	}
	return &bufferedBody{Reader: bytes.NewReader(e.buf), release: func() { p.release(i, e) }}, nil
}

// startAhead makes sure files i+1..i+depth that qualify are being fetched.
func (p *Prefetcher) startAhead(ctx context.Context, i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for j := i + 1; j <= i+p.depth && j < len(p.files); j++ {
		size := int64(p.files[j].size)
		if size <= 0 || size > p.maxFile {
			continue
		}
		if _, started := p.entries[j]; started {
			continue
		}
		if p.used+size > p.budget {
			// Budget is a hard cap, not a queue: whatever does not fit now
			// is fetched live when the writer gets there.
			continue
		}
		p.used += size
		e := &prefetched{size: size, done: make(chan struct{})}
		p.entries[j] = e
		go p.run(ctx, p.url(p.files[j].path), e)
	}
}

func (p *Prefetcher) run(ctx context.Context, url string, e *prefetched) {
	defer close(e.done)
	body, err := p.base.Fetch(ctx, url, 0, -1)
	if err != nil {
		e.err = err
		return
	}
	defer func() { _ = body.Close() }()
	buf := make([]byte, 0, e.size)
	// LimitReader guards the budget against an upstream that ignores the
	// declared size; a short body is detected by the length check below.
	buf, err = readAllInto(buf, io.LimitReader(body, e.size))
	if err != nil {
		e.err = err
		return
	}
	if int64(len(buf)) != e.size {
		e.err = io.ErrUnexpectedEOF
		log.WithField("url", fetch.Redact(url)).WithField("want", e.size).WithField("got", len(buf)).Warn("prefetch: short body, will fetch live")
		return
	}
	e.buf = buf
}

// release returns the entry's memory to the budget once its body has been
// consumed or given up on. Idempotent per entry.
func (p *Prefetcher) release(i int, e *prefetched) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.entries[i]; ok && cur == e {
		delete(p.entries, i)
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
