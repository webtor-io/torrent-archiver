package services

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/webtor-io/lazymap"
	ts "github.com/webtor-io/torrent-store/proto"
)

type TorrentStore struct {
	lazymap.LazyMap[[]file]
	ts *TorrentStoreClient
}

func NewTorrentStore(ts *TorrentStoreClient) *TorrentStore {
	return &TorrentStore{
		ts: ts,
		LazyMap: lazymap.New[[]file](&lazymap.Config{
			Capacity: 100,
		}),
	}
}

// isPadFile reports whether f is BEP 47 padding: filler the encoder put
// between files so each real file starts on a piece boundary. It is not
// content and does not belong in an archive. The "p" attribute when set,
// else the conventional names (".pad/<n>" from libtorrent/qBittorrent,
// "_____padding_file_*" from BitComet).
func isPadFile(f *metainfo.FileInfo) bool {
	if strings.Contains(f.Attr, "p") {
		return true
	}
	p := f.Path
	if len(f.PathUtf8) > 0 {
		p = f.PathUtf8
	}
	if len(p) == 0 {
		return false
	}
	return p[0] == ".pad" || strings.HasPrefix(p[len(p)-1], "_____padding_file")
}

func getPath(info *metainfo.Info, f *metainfo.FileInfo) []string {
	name := info.Name
	if info.NameUtf8 != "" {
		name = info.NameUtf8
	}
	res := []string{name}
	if len(f.PathUtf8) > 0 {
		res = append(res, f.PathUtf8...)
	} else if len(f.Path) > 0 {
		res = append(res, f.Path...)
	}
	return res
}

func (s *TorrentStore) get(ctx context.Context, h string) ([]file, error) {
	c, err := s.ts.Get()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get torrent store client")
	}
	r, err := c.Pull(ctx, &ts.PullRequest{InfoHash: h})
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull torrent from the torrent store")
	}
	reader := bytes.NewReader(r.Torrent)
	mi, err := metainfo.Load(reader)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse torrent")
	}
	log.Info("torrent pulled successfully")
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return nil, err
	}
	var res []file
	for _, f := range info.UpvertedFiles() {
		if isPadFile(&f) {
			continue
		}
		p := getPath(&info, &f)
		path := strings.Join(p, "/")
		res = append(res, file{
			path:     path,
			size:     uint64(f.Length),
			modified: time.Unix(mi.CreationDate, 0),
		})
	}

	return res, nil
}

func (s *TorrentStore) Get(h string) ([]file, error) {
	return s.LazyMap.Get(h, func() ([]file, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.get(ctx, h)
	})
}
