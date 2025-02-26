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
