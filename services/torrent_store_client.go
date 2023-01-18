package services

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	ts "github.com/webtor-io/torrent-store/proto"
	"google.golang.org/grpc"
)

type TorrentStoreClient struct {
	cl     ts.TorrentStoreClient
	host   string
	port   int
	conn   *grpc.ClientConn
	mux    sync.Mutex
	err    error
	inited bool
}

const (
	torrentStoreHostFlag = "torrent-store-host"
	torrentStorePortFlag = "torrent-store-port"
)

func RegisterTorrentStoreClientFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   torrentStoreHostFlag,
			Usage:  "torrent store host",
			Value:  "",
			EnvVar: "TORRENT_STORE_SERVICE_HOST, TORRENT_STORE_HOST",
		},
		cli.IntFlag{
			Name:   torrentStorePortFlag,
			Usage:  "torrent store port",
			Value:  50051,
			EnvVar: "TORRENT_STORE_SERVICE_PORT, TORRENT_STORE_PORT",
		},
	)
}

func NewTorrentStoreClient(c *cli.Context) *TorrentStoreClient {
	return &TorrentStoreClient{
		host: c.String(torrentStoreHostFlag),
		port: c.Int(torrentStorePortFlag),
	}
}

func (s *TorrentStoreClient) get() (ts.TorrentStoreClient, error) {
	log.Info("Initializing TorrentStoreClient")
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	conn, err := grpc.Dial(addr, grpc.WithInsecure())
	s.conn = conn
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial torrent store addr=%v", addr)
	}
	return ts.NewTorrentStoreClient(s.conn), nil
}

func (s *TorrentStoreClient) Get() (ts.TorrentStoreClient, error) {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.inited {
		return s.cl, s.err
	}
	s.cl, s.err = s.get()
	s.inited = true
	return s.cl, s.err
}

func (s *TorrentStoreClient) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
}
