package services

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/webtor-io/torrent-archiver/internal/fetch"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/webtor-io/torrent-archiver/tarhttp"
)

type Tar struct {
	// files is the (possibly selection-filtered) list computed once by the
	// handler; both write passes must consume the same slice so the
	// size-only pass and the streaming pass stay byte-identical.
	files    []file
	infoHash string
	path     string
	baseURL  string
	token    string
	apiKey   string
	suffix   string
	cl       *http.Client
	// prefetch, when enabled, reads upcoming small files ahead of the
	// stream (see Prefetcher); zero value = off.
	prefetch PrefetchConfig
}

// SetPrefetch enables read-ahead of upcoming small files for Write.
func (s *Tar) SetPrefetch(cfg PrefetchConfig) { s.prefetch = cfg }

func NewTar(cl *http.Client, files []file, infoHash string, path string, baseURL string, token string, apiKey string, suffix string) *Tar {
	return &Tar{
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

func (s *Tar) ContentType() string {
	return "application/x-tar"
}

func (s *Tar) header(f file, withURL bool) *tarhttp.FileHeader {
	fh := &tarhttp.FileHeader{
		Name:     strings.TrimPrefix(f.path, s.path+"/"),
		Size:     f.size,
		Modified: f.modified,
	}
	if withURL {
		fh.URL = fileURL(s.baseURL, s.infoHash, f.path, s.suffix, s.token, s.apiKey)
	}
	return fh
}

// write runs the shared folder walk + per-file emission; withURL toggles
// between the size-only pass and the real content-fetching pass so both
// produce byte-identical layout.
func (s *Tar) write(ctx context.Context, tw *tarhttp.Writer, withURL bool) (contentSize int64, err error) {
	fw := newFolderWalker(s.path)
	for _, f := range s.files {
		err = fw.walk(f, func(name string) error {
			log.Debugf("adding folder=%s", name)
			return tw.CreateHeader(ctx, &tarhttp.FileHeader{
				Name:     name,
				Modified: f.modified,
			})
		})
		if err != nil {
			return 0, errors.Wrapf(err, "failed to write folder for %s", f.path)
		}
		// debug-level for the same reason as in zip.go: fires once per
		// archived entry, and the URL carries token+api-key.
		log.Debugf("adding file=%s", f.path)
		if err := tw.CreateHeader(ctx, s.header(f, withURL)); err != nil {
			return 0, errors.Wrapf(err, "failed to write %s", f.path)
		}
		contentSize += int64(f.size)
	}
	return contentSize, nil
}

func (s *Tar) Size(ctx context.Context) (int64, error) {
	var buf bytes.Buffer
	tw := tarhttp.NewWriter(&buf, 0, -1, nil)
	contentSize, err := s.write(ctx, tw, false)
	if err != nil {
		return 0, err
	}
	_ = tw.Close()
	return contentSize + int64(buf.Len()), nil
}

func (s *Tar) Write(ctx context.Context, w io.Writer, start int64, end int64) error {
	tw := tarhttp.NewWriter(w, start, end, s.cl)
	if pf := NewPrefetcher(fetch.HTTP{Client: s.cl}, s.prefetch, planFor(s.files, s.baseURL, s.infoHash, s.suffix, s.token, s.apiKey)); pf != nil {
		tw.SetFetcher(pf)
	}
	log.Infof("start building tar archive for path=%s infoHash=%s", s.path, s.infoHash)
	if _, err := s.write(ctx, tw, true); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return errors.Wrap(err, "failed to finalize tar")
	}
	log.Infof("finish building tar archive for path=%s infoHash=%s", s.path, s.infoHash)
	return nil
}
