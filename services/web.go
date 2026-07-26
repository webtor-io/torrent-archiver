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

// parseRange interprets a Range header against the archive size and returns
// the inclusive byte window plus the response status. Only single ranges are
// honored; an absent, malformed, or multi-range header falls back to the full
// archive with 200 (RFC 7233 permits ignoring Range). A syntactically valid
// but unsatisfiable range yields 416.
func parseRange(rng string, size int64) (begin int64, end int64, status int) {
	begin, end, status = 0, size-1, http.StatusOK
	if rng == "" || !strings.HasPrefix(rng, "bytes=") {
		return
	}
	spec := strings.TrimPrefix(rng, "bytes=")
	if strings.Contains(spec, ",") {
		return
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return
	}
	if parts[0] == "" {
		// suffix range: last N bytes
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return
		}
		if n <= 0 {
			return 0, size - 1, http.StatusRequestedRangeNotSatisfiable
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, http.StatusPartialContent
	}
	b, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return
	}
	if b >= size {
		return 0, size - 1, http.StatusRequestedRangeNotSatisfiable
	}
	e := size - 1
	if parts[1] != "" {
		pe, perr := strconv.ParseInt(parts[1], 10, 64)
		if perr != nil || pe < b {
			return
		}
		if pe < e {
			e = pe
		}
	}
	return b, e, http.StatusPartialContent
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
		name := filepath.Base(r.URL.Path)
		log.Infof("got request with infoHash=%s path=%s name=%s", infoHash, path, name)

		// Format is carried by the requested filename extension
		// (rest-api names the archive <dir>.zip or <dir>.tar), so the
		// proxy chain stays format-agnostic.
		var z Archive
		if strings.HasSuffix(strings.ToLower(name), ".tar") {
			z = NewTar(s.ts, s.cl, infoHash, path, baseURL, token, apiKey, suffix)
		} else {
			z = NewZip(s.ts, s.cl, infoHash, path, baseURL, token, apiKey, suffix)
		}

		size, err := z.Size(r.Context())

		if err != nil {
			log.WithError(err).Error("failed to get archive size")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		begin, end, status := parseRange(r.Header.Get("Range"), size)

		w.Header().Set("Content-Type", z.ContentType())
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Etag", fmt.Sprintf("\"%x\"", sha1.Sum([]byte(infoHash+path))))
		w.Header().Set("Last-Modified", time.Unix(0, 0).Format(http.TimeFormat))

		if status == http.StatusRequestedRangeNotSatisfiable {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%v", end-begin+1))
		if status == http.StatusPartialContent {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %v-%v/%v", begin, end, size))
			w.WriteHeader(status)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		err = z.Write(r.Context(), w, begin, end)
		if err != nil {
			// Response is already committed by the Flush above (status + headers
			// sent, body streaming), so we can't change the status code here —
			// a WriteHeader call now only logs "superfluous response.WriteHeader".
			log.WithError(err).Error("failed to write archive")
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
