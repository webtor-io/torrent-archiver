package main

import (
	"os"

	joonix "github.com/joonix/log"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func main() {
	log.SetFormatter(joonix.NewFormatter())
	app := cli.NewApp()
	app.Name = "torrent-archiver"
	app.Usage = "Generates archive with selected content from torrent"
	app.Version = "0.0.1"
	configure(app)
	err := app.Run(os.Args)
	if err != nil {
		log.WithError(err).Fatal("failed to serve application")
	}
}
