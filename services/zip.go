package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/webtor-io/torrent-archiver/ziphttp"

	"github.com/pkg/errors"

	log "github.com/sirupsen/logrus"
)

type file struct {
	path     string
	size     uint64
	modified time.Time
}

type Zip struct {
	// files is the (possibly selection-filtered) list computed once by the
	// handler; Size and Write must consume the same slice so Content-Length
	// always matches the streamed bytes.
	files    []file
	infoHash string
	path     string
	baseURL  string
	token    string
	apiKey   string
	suffix   string
	cl       *http.Client
}

func (s *Zip) ContentType() string {
	return "application/zip"
}

func NewZip(cl *http.Client, files []file, infoHash string, path string, baseURL string, token string, apiKey string, suffix string) *Zip {
	return &Zip{
		files:    files,
		infoHash: infoHash,
		path:     path,
		baseURL:  baseURL,
		token:    token,
		apiKey:   apiKey,
		suffix:   suffix,
		cl:       cl,
	}
}

func (s *Zip) folderHeader(name string, modified time.Time) *ziphttp.FileHeader {
	return &ziphttp.FileHeader{
		Name:     name,
		Modified: modified,
	}
}

func (s *Zip) writeFile(ctx context.Context, zw *ziphttp.Writer, f file, fw *folderWalker) error {
	err := fw.walk(f, func(name string) error {
		log.Debugf("adding folder=%s", name)
		return zw.CreateHeader(ctx, s.folderHeader(name, f.modified))
	})
	if err != nil {
		return err
	}
	// debug-level: per-file, fires once per archived entry — at info it was ~25 GB/day
	// of Loki ingest on a multi-thousand-file torrent. url omitted: it carries token+api-key.
	log.Debugf("adding file=%s", f.path)
	fh := &ziphttp.FileHeader{
		Name:               strings.TrimPrefix(f.path, s.path+"/"),
		URL:                fileURL(s.baseURL, s.infoHash, f.path, s.suffix, s.token, s.apiKey),
		UncompressedSize64: f.size,
		Modified:           f.modified,
	}
	return zw.CreateHeader(ctx, fh)
}

func (s *Zip) Size(ctx context.Context) (size int64, err error) {
	var buf bytes.Buffer

	zw := ziphttp.NewWriter(&buf, 0, -1, nil)
	fw := newFolderWalker(s.path)
	for _, f := range s.files {
		err = fw.walk(f, func(name string) error {
			return zw.CreateHeader(ctx, s.folderHeader(name, f.modified))
		})
		if err != nil {
			return 0, err
		}
		header := &ziphttp.FileHeader{
			Name:               strings.TrimPrefix(f.path, s.path+"/"),
			Method:             ziphttp.Store,
			UncompressedSize64: f.size,
			Modified:           f.modified,
		}
		cerr := zw.CreateHeader(ctx, header)
		if cerr != nil {
			err = cerr
			_ = zw.Close()
			return
		}

		size += int64(f.size)
	}
	_ = zw.Close()
	size += int64(buf.Len())
	return
}

func (s *Zip) Write(ctx context.Context, w io.Writer, start int64, end int64) error {
	zw := ziphttp.NewWriter(w, start, end, s.cl)
	defer func(zw *ziphttp.Writer) {
		_ = zw.Close()
	}(zw)
	log.Infof("start building archive for path=%s infoHash=%s files=%d", s.path, s.infoHash, len(s.files))
	fw := newFolderWalker(s.path)
	for _, f := range s.files {
		err := s.writeFile(ctx, zw, f, fw)
		if err != nil {
			return errors.Wrapf(err, "failed to write %s", f.path)
		}
	}
	log.Infof("finish building archive for path=%s infoHash=%s", s.path, s.infoHash)
	return nil
}
