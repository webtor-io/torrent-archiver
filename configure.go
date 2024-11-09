package main

import (
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	s "github.com/webtor-io/torrent-archiver/services"
	"net/http"
)

func configure(app *cli.App) {
	app.Flags = []cli.Flag{}
	app.Flags = cs.RegisterProbeFlags(app.Flags)
	app.Flags = cs.RegisterPprofFlags(app.Flags)
	app.Flags = cs.RegisterPromFlags(app.Flags)
	app.Flags = s.RegisterWebFlags(app.Flags)
	app.Flags = s.RegisterTorrentStoreClientFlags(app.Flags)
	app.Action = run
}

func run(c *cli.Context) error {
	var services []cs.Servable
	// Setting ProbeService
	probe := cs.NewProbe(c)
	if probe != nil {
		services = append(services, probe)
		defer probe.Close()
	}

	// Setting PprofService
	pprof := cs.NewPprof(c)
	if pprof != nil {
		services = append(services, pprof)
		defer pprof.Close()
	}

	// Setting PromService
	prom := cs.NewProm(c)
	if prom != nil {
		services = append(services, prom)
		defer prom.Close()
	}

	// Setting TorrentStoreCLient
	torrentStoreClient := s.NewTorrentStoreClient(c)
	defer torrentStoreClient.Close()

	// Setting TorrentStore
	torrentStore := s.NewTorrentStore(torrentStoreClient)

	// Setting HTTP Client
	httpClient := http.DefaultClient

	// Setting WebService
	web := s.NewWeb(c, torrentStore, httpClient)
	services = append(services, web)
	defer web.Close()

	// Setting ServeService
	serve := cs.NewServe(services...)

	// And SERVE!
	err := serve.Serve()
	if err != nil {
		log.WithError(err).Error("got server error")
	}
	return err
}
