package services

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
)

// Archive is a deterministic, range-addressable archive stream: Size is
// computed from the torrent file list alone, and Write can emit any byte
// range [start, end] independently. Implemented by Zip and Tar.
type Archive interface {
	ContentType() string
	Size(ctx context.Context) (int64, error)
	Write(ctx context.Context, w io.Writer, start int64, end int64) error
	// firstRead reports the first file content read Write would make for
	// [start, end] without making it; nil when the window reads no file
	// content (directory entries, the zip central directory).
	firstRead(ctx context.Context, start int64, end int64) *firstRead
}

// firstRead stands in for the fetcher on a dry pass of a writer: it records
// each read and fails it. The content read's failure ends the pass, so the
// last read recorded is the window's first content read. A zip CRC gap read
// of the same file comes before it and is overwritten: Write skips the gap
// when a checkpoint sits at the window's start, and the content read it
// always makes.
type firstRead struct {
	url        string
	begin, end int64
}

var errDryRun = errors.New("dry run: read recorded")

func (f *firstRead) Fetch(_ context.Context, url string, begin, end int64) (io.ReadCloser, error) {
	f.url, f.begin, f.end = url, begin, end
	return nil, errDryRun
}

func (f *firstRead) got() *firstRead {
	if f.url == "" {
		return nil
	}
	return f
}

// fileSource is what the handler needs from the torrent store: the file
// list of a torrent. An interface so handler tests can hand in a fixed
// list instead of standing up the gRPC store.
type fileSource interface {
	Get(infoHash string) ([]file, error)
}

// generateFileList returns the torrent's files under path, in torrent order.
// A non-empty selected list additionally narrows the result to files that
// exactly match a selected path or lie under a selected directory path.
func generateFileList(ts fileSource, infoHash string, path string, selected []string) ([]file, error) {
	files, err := ts.Get(infoHash)
	if err != nil {
		return nil, err
	}
	// Prefixes are precomputed once: the p+"/" concat inside the per-file
	// loop would otherwise allocate O(files × selected) strings.
	prefixes := make([]string, len(selected))
	for n, p := range selected {
		prefixes[n] = p + "/"
	}
	var res []file
	for _, f := range files {
		// Boundary-aware prefix: a bare HasPrefix(f.path, path) would leak
		// sibling entries sharing the name as a string prefix ("T/Season1"
		// matching "T/Season10/e01.mkv").
		if path != "" && f.path != path && !strings.HasPrefix(f.path, path+"/") {
			continue
		}
		if len(selected) > 0 && !matchesAny(f.path, selected, prefixes) {
			continue
		}
		res = append(res, f)
	}
	return res, nil
}

func matchesAny(path string, selected []string, prefixes []string) bool {
	for n, p := range selected {
		if path == p || strings.HasPrefix(path, prefixes[n]) {
			return true
		}
	}
	return false
}

// fileURL builds the authorized proxy URL a writer fetches file content from.
func fileURL(baseURL string, infoHash string, path string, suffix string, token string, apiKey string) string {
	return baseURL + "/" + infoHash + "/" + url.PathEscape(path) + suffix + "?download=true&token=" + token + "&api-key=" + apiKey
}

// folderWalker emits every parent folder of the files it sees exactly once,
// in first-seen order — the shared directory-entry walk for both formats.
type folderWalker struct {
	written []string
	path    string
}

func newFolderWalker(path string) *folderWalker {
	return &folderWalker{path: path}
}

func (s *folderWalker) walk(f file, emit func(name string) error) error {
	parts := strings.Split(strings.TrimPrefix(f.path, s.path+"/"), "/")
	if len(parts) == 1 {
		return nil
	}
	for i := 1; i < len(parts); i++ {
		path := strings.Join(parts[:i], "/")
		found := false
		for _, wr := range s.written {
			if wr == path {
				found = true
			}
		}
		if found {
			continue
		}
		if err := emit(path + "/"); err != nil {
			return err
		}
		s.written = append(s.written, path)
	}
	return nil
}
