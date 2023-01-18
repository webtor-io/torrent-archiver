package services

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	ts "github.com/webtor-io/torrent-store/proto"
)

type MetaInfo struct {
	cl       *TorrentStoreClient
	infoHash string
	mux      sync.Mutex
	mi       *metainfo.MetaInfo
	err      error
	inited   bool
}

func NewMetaInfo(cl *TorrentStoreClient, infohash string) *MetaInfo {
	return &MetaInfo{
		cl:       cl,
		infoHash: infohash,
	}
}

func (s *MetaInfo) Get() (*metainfo.MetaInfo, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.inited {
		return s.mi, s.err
	}
	s.mi, s.err = s.get()
	s.inited = true
	return s.mi, s.err
}

func (s *MetaInfo) get() (*metainfo.MetaInfo, error) {
	log.Info("initializing MetaInfo")
	c, err := s.cl.Get()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get torrent store client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := c.Pull(ctx, &ts.PullRequest{InfoHash: s.infoHash})
	if err != nil {
		return nil, errors.Wrap(err, "failed to pull torrent from the torrent store")
	}
	reader := bytes.NewReader(r.Torrent)
	mi, err := metainfo.Load(reader)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse torrent")
	}
	log.Info("torrent pulled successfully")
	return mi, nil
}
