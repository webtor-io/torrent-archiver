package main

import (
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	s "github.com/webtor-io/torrent-archiver/services"
)

func configure(app *cli.App) {
	app.Flags = []cli.Flag{}
	app.Flags = cs.RegisterProbeFlags(app.Flags)
	app.Flags = cs.RegisterPprofFlags(app.Flags)
	app.Flags = s.RegisterWebFlags(app.Flags)
	app.Flags = s.RegisterTorrentStoreClientFlags(app.Flags)
	app.Action = run
}

func run(c *cli.Context) error {
	// Setting ProbeService
	probe := cs.NewProbe(c)
	defer probe.Close()

	// Setting PprofService
	pprof := cs.NewPprof(c)
	defer pprof.Close()

	// Setting TorrentStoreCLient
	torrentStoreClient := s.NewTorrentStoreClient(c)
	defer torrentStoreClient.Close()

	// Setting TorrentStore
	torrentStore := s.NewTorrentStore(torrentStoreClient)

	// Setting WebService
	web := s.NewWeb(c, torrentStore)
	defer web.Close()

	// Setting ServeService
	serve := cs.NewServe(probe, pprof, web)

	// And SERVE!
	err := serve.Serve()
	if err != nil {
		log.WithError(err).Error("Got server error")
	}
	return err
}
