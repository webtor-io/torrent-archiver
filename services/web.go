package services

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

type Web struct {
	host string
	port int
	ln   net.Listener
	cl   *TorrentStore
}

const (
	WEB_HOST_FLAG = "host"
	WEB_PORT_FLAG = "port"
)

func NewWeb(c *cli.Context, cl *TorrentStore) *Web {
	return &Web{host: c.String(WEB_HOST_FLAG), port: c.Int(WEB_PORT_FLAG), cl: cl}
}

func RegisterWebFlags(c *cli.App) {
	c.Flags = append(c.Flags, cli.StringFlag{
		Name:  WEB_HOST_FLAG,
		Usage: "listening host",
		Value: "",
	})
	c.Flags = append(c.Flags, cli.IntFlag{
		Name:  WEB_PORT_FLAG,
		Usage: "http listening port",
		Value: 8080,
	})
}

func (s *Web) Serve() error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Wrap(err, "Failed to web listen to tcp connection")
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		infoHash := r.Header.Get("X-Info-Hash")
		if infoHash == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		path := r.Header.Get("X-Origin-Path")
		if path == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		suffix := ""
		if strings.Contains(r.Header.Get("X-Path"), "~tc") {
			suffix = "~tc"
		}
		path = strings.TrimLeft(path, "/")
		token := r.Header.Get("X-Token")
		// if token == "" {
		// 	w.WriteHeader(http.StatusBadRequest)
		// 	return
		// }
		apiKey := r.Header.Get("X-Api-Key")
		// if apiKey == "" {
		// 	w.WriteHeader(http.StatusBadRequest)
		// 	return
		// }
		baseURL := r.Header.Get("X-Proxy-Url")
		if baseURL == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		log.Infof("Got request with infoHash=%s path=%s", infoHash, path)
		z := NewZip(s.cl, infoHash, path, baseURL, token, apiKey, suffix)

		size, err := z.Size()

		if err != nil {
			log.WithError(err).Error("Failed to get zip size")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		name := filepath.Base(r.URL.Path)
		log.Infof("Making archive with name=%s", name)

		rng := r.Header.Get("Range")
		begin := 0
		end := int(size - 1)
		clen := size
		if rng != "" {
			parts := strings.Split(strings.TrimPrefix(rng, "bytes="), "-")
			if parts[1] != "" {
				end, err = strconv.Atoi(parts[1])
				if err != nil {
					log.WithError(err).Errorf("Failed to parse range %s", rng)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			begin, err = strconv.Atoi(parts[0])
			if err != nil {
				log.WithError(err).Errorf("Failed to parse range %s", rng)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			clen = int64(end - begin + 1)
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", fmt.Sprintf("%v", clen))
		// log.Info(clen)

		if rng != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %v-%v/%v", begin, end, size))
			w.WriteHeader(http.StatusPartialContent)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		err = z.Write(w, int64(begin), int64(end))
		if err != nil {
			log.WithError(err).Error("Failed to write zip")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	})
	log.Infof("Serving Web at %v", addr)
	return http.Serve(ln, mux)
}

func (s *Web) Close() {
	if s.ln != nil {
		s.ln.Close()
	}
}
