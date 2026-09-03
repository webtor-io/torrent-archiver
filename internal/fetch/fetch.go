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
		return nil, err
	}
	if res.StatusCode >= 300 {
		_ = res.Body.Close()
		return nil, errors.Errorf("got bad http code from url=%v code=%v", url, res.StatusCode)
	}
	return res.Body, nil
}

// Whole reports whether the window denotes the entire file of the given
// size — the only shape a prefetched buffer can satisfy.
func Whole(begin, end, size int64) bool {
	return begin == 0 && (end == -1 || end == size)
}
