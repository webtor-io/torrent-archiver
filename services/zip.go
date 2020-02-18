package services

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
}

func NewZip(ts *TorrentStore, infoHash string, path string, baseURL string, token string, apiKey string) *Zip {
	return &Zip{ts: ts, infoHash: infoHash, path: path, baseURL: baseURL, token: token, apiKey: apiKey}
}

func (s *Zip) writeFile(w io.Writer, zw *zip.Writer, info *metainfo.Info, f *metainfo.FileInfo) error {
	p := "/" + url.QueryEscape(strings.Join(s.getPath(info, f), "/"))
	log.Infof("Adding file=%s", p)
	url := s.baseURL + "/" + s.infoHash + p + "?download=true&token=" + s.token + "&api-key=" + s.apiKey
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New(fmt.Sprintf("Bad status code %d", res.StatusCode))
	}
	// fw, err := zw.Create(strings.Join(s.getPath(info, f), "/"))

	fw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   (strings.Join(s.getPath(info, f), "/")),
		Method: zip.Store,
	})
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, res.Body)
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

func (s *Zip) Write(w io.Writer) error {
	mi, err := s.ts.Get(s.infoHash)
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
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
