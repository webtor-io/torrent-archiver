package main

import (
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	s "github.com/webtor-io/torrent-archiver/services"
)

func configure(app *cli.App) {
	app.Flags = []cli.Flag{}
	cs.RegisterProbeFlags(app)
	s.RegisterWebFlags(app)
	s.RegisterTorrentStoreClientFlags(app)
	app.Action = run
}

func run(c *cli.Context) error {
	// Setting ProbeService
	probe := cs.NewProbe(c)
	defer probe.Close()

	// Setting TorrentStoreCLient
	torrentStoreClient := s.NewTorrentStoreClient(c)
	defer torrentStoreClient.Close()

	// Setting TorrentStore
	torrentStore := s.NewTorrentStore(torrentStoreClient)

	// Setting WebService
	web := s.NewWeb(c, torrentStore)
	defer web.Close()

	// Setting ServeService
	serve := cs.NewServe(probe, web)

	// And SERVE!
	err := serve.Serve()
	if err != nil {
		log.WithError(err).Error("Got server error")
	}
	return err
}
