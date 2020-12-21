package services

import (
	"bytes"
	"io"
	"strings"

	"github.com/webtor-io/torrent-archiver/zip"

	"github.com/pkg/errors"

	"github.com/anacrolix/torrent/metainfo"

	log "github.com/sirupsen/logrus"
)

type Zip struct {
	ts       *TorrentStore
	infoHash string
	path     string
	baseURL  string
	token    string
	apiKey   string
	suffix   string
}

func NewZip(ts *TorrentStore, infoHash string, path string, baseURL string, token string, apiKey string, suffix string) *Zip {
	return &Zip{ts: ts, infoHash: infoHash, path: path, baseURL: baseURL, token: token, apiKey: apiKey, suffix: suffix}
}

func (s *Zip) writeFile(w io.Writer, zw *zip.Writer, info *metainfo.Info, f *metainfo.FileInfo) error {
	p := "/" + strings.Join(s.getPath(info, f), "/")
	url := s.baseURL + "/" + s.infoHash + p + s.suffix + "?download=true&token=" + s.token + "&api-key=" + s.apiKey
	// log.Infof("Adding file=%s url=%s", p, url)
	_, err := zw.CreateHeader(&zip.FileHeader{
		Name:   (strings.Join(s.getPath(info, f), "/")),
		URL:    url,
		Length: f.Length,
	})
	if err != nil {
		return err
	}
	return nil
}

func (s *Zip) getPath(info *metainfo.Info, f *metainfo.FileInfo) []string {
	res := []string{info.Name}
	if len(f.Path) > 0 {
		res = append(res, f.Path...)
	}
	return res
}

func (s *Zip) Size() (size int64, err error) {
	mi, err := s.ts.Get(s.infoHash)
	if err != nil {
		return
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf, 0, -1, nil)
	for _, f := range info.UpvertedFiles() {
		p := "/" + strings.Join(s.getPath(&info, &f), "/")
		_, cerr := zw.CreateHeader(&zip.FileHeader{
			Name:   p,
			Method: zip.Store,
		})
		if cerr != nil {
			err = cerr
			zw.Close()
			return
		}
		if strings.HasPrefix("/"+p, s.path) {
			size += f.Length - 2 // "-2" was find by doing some tests, there is some unknown magic
		}
	}
	zw.Close()
	size += int64(buf.Len())
	return
}

func (s *Zip) Write(w io.Writer, start int64, end int64) error {
	mi, err := s.ts.Get(s.infoHash)
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w, start, end, nil)
	defer zw.Close()
	log.Infof("Start building archive for path=%s infoHash=%s", s.path, s.infoHash)
	log.Info(s.path)
	for _, f := range info.UpvertedFiles() {
		p := "/" + strings.Join(s.getPath(&info, &f), "/")
		if strings.HasPrefix(p, s.path) {
			err := s.writeFile(w, zw, &info, &f)
			if err != nil {
				errors.Wrapf(err, "Failed to write %s", p)
			}
		}
	}
	log.Infof("Finish building archive for path=%s infoHash=%s", s.path, s.infoHash)
	return nil
}
