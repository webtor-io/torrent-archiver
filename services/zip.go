package services

import (
	"bytes"
	"context"
	"github.com/webtor-io/torrent-archiver/ziphttp"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"

	log "github.com/sirupsen/logrus"
)

type file struct {
	path     string
	size     uint64
	modified time.Time
}

type Zip struct {
	ts       *TorrentStore
	infoHash string
	path     string
	baseURL  string
	token    string
	apiKey   string
	suffix   string
	cl       *http.Client
}

type folderWriter struct {
	written []string
	path    string
}

func newFolderWriter(path string) *folderWriter {
	return &folderWriter{
		written: []string{},
		path:    path,
	}
}

func (s *folderWriter) write(ctx context.Context, zw *ziphttp.Writer, f file) error {
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
		log.Infof("adding folder=%s", path)
		fh := &ziphttp.FileHeader{
			Name:     path + "/",
			Modified: f.modified,
		}
		err := zw.CreateHeader(ctx, fh)
		if err != nil {
			return err
		}
		s.written = append(s.written, path)
	}
	return nil
}

func NewZip(ts *TorrentStore, cl *http.Client, infoHash string, path string, baseURL string, token string, apiKey string, suffix string) *Zip {
	return &Zip{
		ts:       ts,
		infoHash: infoHash,
		path:     path,
		baseURL:  baseURL,
		token:    token,
		apiKey:   apiKey,
		suffix:   suffix,
		cl:       cl,
	}
}

func (s *Zip) writeFile(ctx context.Context, zw *ziphttp.Writer, f file, fw *folderWriter) error {
	path := f.path
	err := fw.write(ctx, zw, f)
	if err != nil {
		return err
	}
	url := s.baseURL + "/" + s.infoHash + "/" + url.PathEscape(path) + s.suffix + "?download=true&token=" + s.token + "&api-key=" + s.apiKey
	log.Infof("Adding file=%s url=%s", path, url)
	fh := &ziphttp.FileHeader{
		Name:               strings.TrimPrefix(path, s.path+"/"),
		URL:                url,
		UncompressedSize64: f.size,
		Modified:           f.modified,
	}
	err = zw.CreateHeader(ctx, fh)
	if err != nil {
		return err
	}
	return nil
}

func (s *Zip) Size(ctx context.Context) (size int64, err error) {
	files, err := s.generateFileList()
	if err != nil {
		return
	}
	var buf bytes.Buffer

	zw := ziphttp.NewWriter(&buf, 0, -1, nil)
	fw := newFolderWriter(s.path)
	for _, f := range files {
		err = fw.write(ctx, zw, f)
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
func (s *Zip) generateFileList() ([]file, error) {
	files, err := s.ts.Get(s.infoHash)
	if err != nil {
		return nil, err
	}
	var res []file
	for _, f := range files {
		if strings.HasPrefix(f.path, s.path) {
			res = append(res, f)
		}
	}
	return res, nil
}

func (s *Zip) Write(ctx context.Context, w io.Writer, start int64, end int64) error {
	zw := ziphttp.NewWriter(w, start, end, s.cl)
	defer func(zw *ziphttp.Writer) {
		_ = zw.Close()
	}(zw)
	log.Infof("start building archive for path=%s infoHash=%s", s.path, s.infoHash)
	log.Info(s.path)
	files, err := s.generateFileList()
	if err != nil {
		return errors.Wrap(err, "failed to generate file list")
	}
	fw := newFolderWriter(s.path)
	for _, f := range files {
		err := s.writeFile(ctx, zw, f, fw)
		if err != nil {
			return errors.Wrapf(err, "failed to write %s", f.path)
		}
	}
	log.Infof("finish building archive for path=%s infoHash=%s", s.path, s.infoHash)
	return nil
}
