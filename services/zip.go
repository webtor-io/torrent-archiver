package services

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/webtor-io/torrent-archiver/zip"

	"github.com/pkg/errors"

	"github.com/anacrolix/torrent/metainfo"

	log "github.com/sirupsen/logrus"
)

type file struct {
	path      string
	size      uint64
	modified  time.Time
	pathParts []string
}

type Zip struct {
	ts       *TorrentStore
	infoHash string
	path     string
	baseURL  string
	token    string
	apiKey   string
	suffix   string
}

type folderWriter struct {
	written []string
	path    string
}

func newFolderWriter(path string) *folderWriter {
	return &folderWriter{written: []string{}, path: path}
}

func (s *folderWriter) write(zw *zip.Writer, f file) error {
	parts := f.pathParts
	if len(parts) == 1 {
		return nil
	}
	for i := 1; i < len(parts); i++ {
		path := strings.Join(parts[:i], "/")
		if strings.HasPrefix(s.path, path) {
			continue
		}
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
		fh := &zip.FileHeader{
			Name:     path + "/",
			Modified: f.modified,
		}
		err := zw.CreateHeader(fh)
		if err != nil {
			return err
		}
		s.written = append(s.written, path)
	}
	return nil
}

func NewZip(ts *TorrentStore, infoHash string, path string, baseURL string, token string, apiKey string, suffix string) *Zip {
	return &Zip{
		ts:       ts,
		infoHash: infoHash,
		path:     path,
		baseURL:  baseURL,
		token:    token,
		apiKey:   apiKey,
		suffix:   suffix,
	}
}

func (s *Zip) writeFile(zw *zip.Writer, f file, fw *folderWriter) error {
	path := f.path
	err := fw.write(zw, f)
	if err != nil {
		return err
	}
	url := s.baseURL + "/" + s.infoHash + "/" + url.PathEscape(path) + s.suffix + "?download=true&token=" + s.token + "&api-key=" + s.apiKey
	log.Infof("Adding file=%s url=%s", path, url)
	fh := &zip.FileHeader{
		Name:               strings.TrimPrefix(path, s.path+"/"),
		URL:                url,
		UncompressedSize64: f.size,
		Modified:           f.modified,
	}
	err = zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	return nil
}

func getPath(info *metainfo.Info, f *metainfo.FileInfo) []string {
	res := []string{info.Name}
	if len(f.Path) > 0 {
		res = append(res, f.Path...)
	}
	return res
}

func (s *Zip) Size() (size int64, err error) {
	files, err := s.generateFileList()
	if err != nil {
		return
	}
	var buf bytes.Buffer

	zw := zip.NewWriter(&buf, 0, -1, nil)
	fw := newFolderWriter(s.path)
	for _, f := range files {
		err = fw.write(zw, f)
		if err != nil {
			return 0, err
		}
		header := &zip.FileHeader{
			Name:               strings.TrimPrefix(f.path, s.path+"/"),
			Method:             zip.Store,
			UncompressedSize64: f.size,
			Modified:           f.modified,
		}
		cerr := zw.CreateHeader(header)
		if cerr != nil {
			err = cerr
			zw.Close()
			return
		}

		size += int64(f.size)
	}
	zw.Close()
	size += int64(buf.Len())
	return
}
func (s *Zip) generateFileList() ([]file, error) {
	mi, err := s.ts.Get(s.infoHash)
	if err != nil {
		return nil, err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, err
	}
	var res []file
	for _, f := range info.UpvertedFiles() {
		p := getPath(&info, &f)
		path := strings.Join(p, "/")
		if strings.HasPrefix(path, s.path) {
			res = append(res, file{
				path:      path,
				pathParts: p,
				size:      uint64(f.Length),
				modified:  time.Unix(mi.CreationDate, 0),
			})
		}
	}
	return res, nil
}

func (s *Zip) Write(w io.Writer, start int64, end int64) error {
	zw := zip.NewWriter(w, start, end, nil)
	defer zw.Close()
	log.Infof("start building archive for path=%s infoHash=%s", s.path, s.infoHash)
	log.Info(s.path)
	files, err := s.generateFileList()
	if err != nil {
		return errors.Wrap(err, "failed to generate file list")
	}
	fw := newFolderWriter(s.path)
	for _, f := range files {
		err := s.writeFile(zw, f, fw)
		if err != nil {
			return errors.Wrapf(err, "failed to write %s", f.path)
		}
	}
	log.Infof("finish building archive for path=%s infoHash=%s", s.path, s.infoHash)
	return nil
}
