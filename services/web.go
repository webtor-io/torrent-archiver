package services

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	cs "github.com/webtor-io/common-services"
	"github.com/webtor-io/lazymap"

	"github.com/webtor-io/torrent-archiver/internal/fetch"
)

type Web struct {
	host            string
	port            int
	apiKey          string
	apiSecret       string
	torrentProxyUrl string
	ln              net.Listener
	ts              fileSource
	cl              *http.Client
	crcs            *CRCStore
	prefetch        PrefetchConfig
	// refusals keeps upstream's answer to the probe of an archive's first
	// read (see refusal), keyed by that read's URL.
	refusals *lazymap.LazyMap[int]
	// gs drains in-flight requests on Close, up to WEB_SHUTDOWN_TIMEOUT,
	// instead of dropping them with the listener.
	gs *cs.GracefulServer
}

const (
	webHostFlag         = "host"
	webPortFlag         = "port"
	apiKeyFlag          = "api-key"
	apiSecretFlag       = "api-secret"
	torrentProxyUrlFlag = "proxy-url"
)

func NewWeb(c *cli.Context, ts *TorrentStore, cl *http.Client, crcs *CRCStore) *Web {
	return &Web{
		host:            c.String(webHostFlag),
		port:            c.Int(webPortFlag),
		ts:              ts,
		cl:              cl,
		crcs:            crcs,
		apiKey:          c.String(apiKeyFlag),
		apiSecret:       c.String(apiSecretFlag),
		torrentProxyUrl: c.String(torrentProxyUrlFlag),
		prefetch:        PrefetchConfigFromCLI(c),
		refusals:        newRefusals(refusalTTL, refusalCap),
		gs:              cs.NewGracefulServer(cs.ShutdownTimeout(c)),
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

const (
	// refusalTTL is how long a refusal is answered from memory: long
	// enough to absorb a download manager retrying every range at once,
	// short enough that a torrent upstream serves again is archived again
	// within a minute.
	refusalTTL = time.Minute
	// refusalCap bounds the memory: one entry per distinct first-read URL
	// probed within refusalTTL, each holding that URL (token included).
	refusalCap = 1000
)

// probeTimeout caps the silence the probe adds before the status line.
// Past it the archive starts as it did before there was a probe. A var for
// the test.
var probeTimeout = 5 * time.Second

// refusedStatuses are upstream answers a retry does not change.
var refusedStatuses = []int{http.StatusForbidden, http.StatusNotFound, http.StatusUnavailableForLegalReasons}

func newRefusals(ttl time.Duration, capacity int) *lazymap.LazyMap[int] {
	return lazymap.New[int](&lazymap.Config{
		Expire:   ttl,
		Capacity: capacity,
		// lazymap runs at most Concurrency loads at once, 10 by default:
		// every archive start would queue behind ten slow probes.
		Concurrency: capacity,
	})
}

// refusal returns the status upstream refuses the window's first read with
// (refusedStatuses), or 0 to go ahead. The probe's answer is kept for
// refusalTTL under that read's URL: the token in it is what upstream
// decides on (a private torrent is refused to one user and served to its
// owner), and the path keeps a 404 for one file from refusing windows that
// never read it. Any other failure is neither kept nor a refusal: the
// archive starts as before, and a transient error stays transient.
//
// A HEAD (head) is answered from memory only. Probed on a miss, it would
// make the seeder load the torrent and keep its readahead for a client that
// may never GET (a link checker, a size query), where a HEAD otherwise
// touches no upstream. Its GET is probed anyway; an entry expiring between
// Status and Get costs one probe.
func (s *Web) refusal(ctx context.Context, z Archive, begin, end int64, head bool) int {
	fr := z.firstRead(ctx, begin, end)
	if fr == nil {
		return 0
	}
	if head {
		if st, ok := s.refusals.Status(fr.url); !ok || st != lazymap.Done {
			return 0
		}
	}
	code, _ := s.refusals.Get(fr.url, func() (int, error) { return s.probe(fr) })
	return code
}

// probe asks upstream about the read with HEAD: the URL, token and Range
// of the GET Write would send, answered without a body and without waiting
// for the swarm. Off the request's context: concurrent requests for the
// same URL share it, and the one that started it may leave first.
func (s *Web) probe(fr *firstRead) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	body, err := fetch.HTTP{Client: s.cl, Method: http.MethodHead}.Fetch(ctx, fr.url, fr.begin, fr.end)
	if err == nil {
		_ = body.Close()
		return 0, nil
	}
	var fe *fetch.Error
	if errors.As(err, &fe) && slices.Contains(refusedStatuses, fe.Status) {
		log.WithError(err).Info("upstream refuses the archive's first file, refusing the archive")
		return fe.Status, nil
	}
	return 0, err
}

// maxSelectedPaths bounds the ?paths= selection; a checked folder covers
// its whole subtree with a single path, and rest-api additionally caps the
// selection's encoded byte length, so a legitimate selection stays far
// below this.
const maxSelectedPaths = 1024

// parseSelectedPaths normalizes the repeated ?paths= query values into
// sorted torrent-rooted paths (no leading slash, as in metainfo file
// lists). Sorting makes the selection a set: the ETag and any downstream
// cache key stay identical however the client ordered the values. ok is
// false when the selection exceeds maxSelectedPaths or smuggles a NUL byte
// (which would alias distinct selections in the NUL-joined ETag).
func parseSelectedPaths(values []string) (selected []string, ok bool) {
	if len(values) > maxSelectedPaths {
		return nil, false
	}
	for _, v := range values {
		if strings.ContainsRune(v, 0) {
			return nil, false
		}
		v = strings.Trim(v, "/")
		if v == "" {
			continue
		}
		selected = append(selected, v)
	}
	sort.Strings(selected)
	return slices.Compact(selected), true
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

// archiveFormat maps the requested filename to the archive format. Format
// is carried by the extension (rest-api names the archive <dir>.zip or
// <dir>.tar), so the proxy chain stays format-agnostic; zip is the default.
func archiveFormat(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".tar") {
		return formatTar
	}
	return formatZip
}

// Listen binds the web port. It runs before any servable starts: the probe
// answers Ready as soon as it listens, and a pod must not read Ready with
// its web port unbound.
func (s *Web) Listen() error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.Wrap(err, "failed to web listen to tcp connection")
	}
	s.ln = ln
	return nil
}

func (s *Web) Serve() error {
	if s.ln == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/", s)
	log.Infof("serving Web at %v", s.ln.Addr())
	return s.gs.Serve(&http.Server{Handler: mux}, s.ln)
}

// ServeHTTP is the archive endpoint. It is a method rather than a closure
// inside Serve so tests can drive it through httptest without a listener.
func (s *Web) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var err error
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
	selected, ok := parseSelectedPaths(r.URL.Query()["paths"])
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	name := filepath.Base(r.URL.Path)
	format := archiveFormat(name)
	log.Infof("got request with infoHash=%s path=%s name=%s selected=%d", infoHash, path, name, len(selected))

	// From here the request is an archive attempt and is accounted
	// for: the gauge spans the store lookup and Size as well, since
	// the request already holds resources during them. The 404/416
	// exits below are client-side mistakes, not archive outcomes,
	// and stay out of archives_total; so does a HEAD past Size (headers only)
	// and a refusal, which never started an archive (archives_refused_total).
	started := time.Now()
	archivesActive.Inc()
	defer archivesActive.Dec()

	// The file list is computed exactly once per request and shared by
	// Size and Write — resumable downloads issue many Range requests
	// and must never pay (or drift between) repeated filter passes.
	files, err := generateFileList(s.ts, infoHash, path, selected)
	if err != nil {
		log.WithError(err).Error("failed to generate file list")
		recordArchive(format, outcomeError, started)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// A selection that intersects the torrent to nothing (stale link,
	// mistyped path) must not silently produce an empty archive.
	if len(selected) > 0 && len(files) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var z Archive
	if format == formatTar {
		t := NewTar(s.cl, files, infoHash, path, baseURL, token, apiKey, suffix)
		t.SetPrefetch(s.prefetch)
		z = t
	} else {
		zp := NewZip(s.cl, files, infoHash, path, baseURL, token, apiKey, suffix, s.crcs)
		zp.SetPrefetch(s.prefetch)
		z = zp
	}

	size, err := z.Size(r.Context())

	if err != nil {
		log.WithError(err).Error("failed to get archive size")
		recordArchive(format, outcomeError, started)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	begin, end, status := parseRange(r.Header.Get("Range"), size)

	// What upstream refuses (a blocked torrent, another user's private
	// one) is refused here, before the status line. Started as an archive,
	// it sent 206 and the first entry's header, failed on the first file,
	// and a download manager retried every range at once.
	if status != http.StatusRequestedRangeNotSatisfiable {
		if code := s.refusal(r.Context(), z, begin, end, r.Method == http.MethodHead); code != 0 {
			archivesRefused.WithLabelValues(strconv.Itoa(code)).Inc()
			http.Error(w, http.StatusText(code), code)
			return
		}
	}

	w.Header().Set("Content-Type", z.ContentType())
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	w.Header().Set("Accept-Ranges", "bytes")
	// Selection participates in the ETag so differently-filtered
	// archives of the same path never validate against each other;
	// the no-selection ETag stays byte-identical to the old scheme.
	etagSrc := infoHash + path
	if len(selected) > 0 {
		etagSrc += "?paths=" + strings.Join(selected, "\x00")
	}
	w.Header().Set("Etag", fmt.Sprintf("\"%x\"", sha1.Sum([]byte(etagSrc))))
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
	// HEAD stops at the headers. net/http silently discards body writes on
	// HEAD, so going on would build the whole archive and read every file
	// from upstream for a client that receives none of it. No archive
	// outcome either: nothing was downloaded.
	if r.Method == http.MethodHead {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	err = z.Write(r.Context(), &meteredWriter{w: w, c: bytesTotal.WithLabelValues(format)}, begin, end)
	outcome := archiveOutcome(r.Context(), err)
	recordArchive(format, outcome, started)
	if err != nil {
		// Response is already committed by the Flush above (status + headers
		// sent, body streaming), so we can't change the status code here —
		// a WriteHeader call now only logs "superfluous response.WriteHeader".
		// A client that hung up is the common case (most aborted
		// downloads) and no fault of ours, so it is not an error-level
		// event; the metric carries the split.
		entry := log.WithError(err).WithField("outcome", outcome)
		if outcome == outcomeClientGone {
			entry.Info("failed to write archive")
		} else {
			entry.Error("failed to write archive")
		}
		return
	}
}

// Close drains in-flight archive downloads (up to WEB_SHUTDOWN_TIMEOUT)
// instead of cutting them with the listener: an archive is one long
// response, and a pod that exits mid-stream leaves the client with a broken
// download. Clients that get cut at the deadline resume with a Range
// request. Close must run before the torrent-store and Redis clients the
// handlers use are closed.
func (s *Web) Close() {
	if s.gs != nil {
		s.gs.Close()
		return
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
}
