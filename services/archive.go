package services

import (
	"context"
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
}

// generateFileList returns the torrent's files under path, in torrent order.
func generateFileList(ts *TorrentStore, infoHash string, path string) ([]file, error) {
	files, err := ts.Get(infoHash)
	if err != nil {
		return nil, err
	}
	var res []file
	for _, f := range files {
		if strings.HasPrefix(f.path, path) {
			res = append(res, f)
		}
	}
	return res, nil
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
