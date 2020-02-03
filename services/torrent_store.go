package services

import (
	"sync"

	"github.com/anacrolix/torrent/metainfo"
)

type TorrentStore struct {
	sm sync.Map
	cl *TorrentStoreClient
}

func NewTorrentStore(cl *TorrentStoreClient) *TorrentStore {
	return &TorrentStore{cl: cl}
}

func (s *TorrentStore) Get(infoHash string) (*metainfo.MetaInfo, error) {
	v, loaded := s.sm.LoadOrStore(infoHash, NewMetaInfo(s.cl, infoHash))
	if !loaded {
		defer s.sm.Delete(infoHash)
	}
	return v.(*MetaInfo).Get()
}
