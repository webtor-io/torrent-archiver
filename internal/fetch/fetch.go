// Package fetch is the one seam between the archive writers and the bytes
// they stream: a Fetcher opens a byte range of an upstream URL. The plain
// HTTP implementation lives here; services.Prefetcher wraps it to read
// upcoming small files ahead of the writer's strictly sequential output.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

// Error marks a failure that came from upstream — the GET itself, its
// status, or a read of its body — so the archiver can tell a proxy/swarm
// problem apart from its own faults and from the client hanging up.
// io.EOF is never wrapped: it is the normal end of a body, not a failure.
type Error struct {
	Err error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Fetcher opens [begin, end) of url. end == -1 means "to the end of the
// file"; begin == 0 && end == -1 is the whole file. The caller closes the
// returned body.
type Fetcher interface {
	Fetch(ctx context.Context, url string, begin, end int64) (io.ReadCloser, error)
}

// HTTP fetches with a plain GET, adding a Range header for partial windows.
type HTTP struct {
	Client *http.Client
}

func (h HTTP) Fetch(ctx context.Context, url string, begin, end int64) (io.ReadCloser, error) {
	cl := h.Client
	if cl == nil {
		cl = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if begin != 0 || end != -1 {
		if end == -1 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", begin))
		} else {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", begin, end-1))
		}
	}
	res, err := cl.Do(req)
	if err != nil {
		return nil, &Error{Err: err}
	}
	if res.StatusCode >= 300 {
		_ = res.Body.Close()
		return nil, &Error{Err: errors.Errorf("got bad http code from url=%v code=%v", url, res.StatusCode)}
	}
	return body{res.Body}, nil
}

// body tags read failures as upstream's: io.Copy into the client returns
// read and write errors alike, and only the type tells them apart.
type body struct {
	io.ReadCloser
}

func (b body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		err = &Error{Err: err}
	}
	return n, err
}

// Whole reports whether the window denotes the entire file of the given
// size — the only shape a prefetched buffer can satisfy.
func Whole(begin, end, size int64) bool {
	return begin == 0 && (end == -1 || end == size)
}
