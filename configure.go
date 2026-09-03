package main

import (
	"net"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	s "github.com/webtor-io/torrent-archiver/services"
)

func configure(app *cli.App) {
	app.Flags = []cli.Flag{}
	app.Flags = cs.RegisterProbeFlags(app.Flags)
	app.Flags = cs.RegisterPprofFlags(app.Flags)
	app.Flags = cs.RegisterPromFlags(app.Flags)
	app.Flags = s.RegisterWebFlags(app.Flags)
	app.Flags = s.RegisterTorrentStoreClientFlags(app.Flags)
	app.Flags = s.RegisterCRCStoreFlags(app.Flags)
	app.Flags = s.RegisterPrefetchFlags(app.Flags)
	app.Flags = cs.RegisterRedisClientFlags(app.Flags)
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

	// Setting HTTP Client. Upstream is torrent-http-proxy; with prefetching
	// several bodies are open per archive at once, so the idle pool must hold
	// more than DefaultTransport's two connections per host. No response
	// header timeout on purpose: a cold swarm legitimately takes long to
	// produce the first byte, and cutting it would recreate the very
	// retry loop the prefetcher exists to avoid.
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          128,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}

	// Setting CRCStore (optional: correct checksums for resumed zip downloads)
	var crcStore *s.CRCStore
	if s.CRCCacheEnabled(c) {
		redisClient := cs.NewRedisClient(c)
		defer redisClient.Close()
		crcStore = s.NewCRCStore(redisClient.Get())
		log.Info("zip crc cache enabled")
	}

	// Setting WebService
	web := s.NewWeb(c, torrentStore, httpClient, crcStore)
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
