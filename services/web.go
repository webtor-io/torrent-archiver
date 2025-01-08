package services

import (
	"crypto/sha1"
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

type Web struct {
	host            string
	port            int
	apiKey          string
	apiSecret       string
	torrentProxyUrl string
	ln              net.Listener
	ts              *TorrentStore
	cl              *http.Client
}

const (
	webHostFlag         = "host"
	webPortFlag         = "port"
	apiKeyFlag          = "api-key"
	apiSecretFlag       = "api-secret"
	torrentProxyUrlFlag = "proxy-url"
)

func NewWeb(c *cli.Context, ts *TorrentStore, cl *http.Client) *Web {
	return &Web{
		host:            c.String(webHostFlag),
		port:            c.Int(webPortFlag),
		ts:              ts,
		cl:              cl,
		apiKey:          c.String(apiKeyFlag),
		apiSecret:       c.String(apiSecretFlag),
		torrentProxyUrl: c.String(torrentProxyUrlFlag),
	}
}

func RegisterWebFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.StringFlag{
			Name:   webHostFlag,
			Usage:  "listening host",
			Value:  "",
			EnvVar: "WEB_HOST",
		},
		cli.IntFlag{
			Name:   webPortFlag,
			Usage:  "http listening port",
			Value:  8080,
			EnvVar: "WEB_PORT",
		},
		cli.StringFlag{
			Name:   apiKeyFlag,
			Usage:  "api key",
			EnvVar: "API_KEY",
		},
		cli.StringFlag{
			Name:   apiSecretFlag,
			Usage:  "api secret",
			EnvVar: "API_SECRET",
		},
		cli.StringFlag{
			Name:   torrentProxyUrlFlag,
			Usage:  "torrent proxy url",
			EnvVar: "TORRENT_PROXY_URL",
		},
	)
}

func (s *Web) Serve() error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Wrap(err, "failed to web listen to tcp connection")
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		infoHash := r.Header.Get("X-Info-Hash")
		if infoHash == "" && r.URL.Query().Get("infohash") != "" {
			infoHash = strings.ToLower(r.URL.Query().Get("infohash"))
		}

		if infoHash == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		path := r.Header.Get("X-Origin-Path")
		if path == "" {
			path = "/"
		}
		suffix := ""
		path = strings.TrimLeft(path, "/")
		token := r.Header.Get("X-Token")
		if token == "" && s.apiSecret != "" {
			t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
			token, err = t.SignedString([]byte(s.apiSecret))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		if token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		apiKey := r.Header.Get("X-Api-Key")
		if apiKey == "" && s.apiKey != "" {
			apiKey = s.apiKey
		}
		if apiKey == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		baseURL := r.Header.Get("X-Proxy-Url")
		if baseURL == "" && s.torrentProxyUrl != "" {
			baseURL = s.torrentProxyUrl
		}
		if baseURL == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		log.Infof("got request with infoHash=%s path=%s", infoHash, path)
		z := NewZip(s.ts, s.cl, infoHash, path, baseURL, token, apiKey, suffix)

		size, err := z.Size(r.Context())

		if err != nil {
			log.WithError(err).Error("failed to get zip size")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		name := filepath.Base(r.URL.Path)
		log.Infof("making archive with name=%s", name)

		rng := r.Header.Get("Range")
		begin := 0
		end := int(size - 1)
		clen := size
		if rng != "" {
			parts := strings.Split(strings.TrimPrefix(rng, "bytes="), "-")
			if parts[1] != "" {
				end, err = strconv.Atoi(parts[1])
				if err != nil {
					log.WithError(err).Errorf("failed to parse range %s", rng)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			begin, err = strconv.Atoi(parts[0])
			if err != nil {
				log.WithError(err).Errorf("failed to parse range %s", rng)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			clen = int64(end - begin + 1)
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", fmt.Sprintf("%v", clen))
		w.Header().Set("Etag", fmt.Sprintf("\"%x\"", sha1.Sum([]byte(infoHash+path))))
		w.Header().Set("Last-Modified", time.Unix(0, 0).Format(http.TimeFormat))

		if rng != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %v-%v/%v", begin, end, size))
			w.WriteHeader(http.StatusPartialContent)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		err = z.Write(r.Context(), w, int64(begin), int64(end))
		if err != nil {
			log.WithError(err).Error("failed to write zip")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	})
	log.Infof("serving Web at %v", addr)
	return http.Serve(ln, mux)
}

func (s *Web) Close() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
}
