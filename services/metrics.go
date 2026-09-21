package services

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/webtor-io/torrent-archiver/internal/fetch"
)

// Metrics register on the default registry, which cs.NewProm already
// serves on the prom port — nothing else needs wiring.
//
// Why these and not the log lines: the archiver's error log is dominated
// by clients hanging up mid-download (about 95% of "failed to write"), so
// counting log lines says nothing about the service. Outcomes split the
// client's behaviour from upstream's and ours, and the active gauge is
// what an OOM kill correlates with — each in-flight archive may hold up to
// the prefetch budget in memory.
var (
	archivesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "archiver",
		Name:      "archives_total",
		Help: "Archive requests that reached the file list stage, by format and outcome. " +
			"client_gone is the client hanging up mid-download and is not a service error; " +
			"upstream_error is the proxy/seeder GET failing; error is everything else (torrent store, size).",
	}, []string{"format", "outcome"})

	archivesActive = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "archiver",
		Name:      "archives_active",
		Help:      "Archive requests currently being served, from file list lookup to the last byte.",
	})

	bytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "archiver",
		Name:      "bytes_total",
		Help:      "Archive bytes written to clients, by format.",
	}, []string{"format"})

	archiveDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "archiver",
		Name:      "archive_duration_seconds",
		Help: "Wall time of an archive request from file list lookup to the end of streaming, " +
			"whatever the outcome. The 30 s bucket edge is the download-manager timeout: a pile-up just under it means clients are giving up on a silent swarm.",
		Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"format"})

	fileFetchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "archiver",
		Name:      "file_fetch_duration_seconds",
		Help: "Time until the upstream GET for one file returns its headers — the swarm's time to first byte. " +
			"The body then streams at the client's pace, which is why it is not included.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 20, 30, 60, 120},
	})

	fileFetchesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "archiver",
		Name:      "file_fetches_total",
		Help:      "Upstream GETs for file content, prefetches included. canceled is the request context ending first (client gone).",
	}, []string{"outcome"})
)

const (
	outcomeOK            = "ok"
	outcomeClientGone    = "client_gone"
	outcomeUpstreamError = "upstream_error"
	outcomeError         = "error"

	fetchOK       = "ok"
	fetchCanceled = "canceled"
	fetchError    = "error"

	formatZip = "zip"
	formatTar = "tar"
)

func init() {
	// Pre-create every label combination so a series exists at 0 from the
	// first scrape; rate() over an absent series is silently empty, which
	// is exactly how the OOM kills went unnoticed.
	for _, f := range []string{formatZip, formatTar} {
		for _, o := range []string{outcomeOK, outcomeClientGone, outcomeUpstreamError, outcomeError} {
			archivesTotal.WithLabelValues(f, o)
		}
		bytesTotal.WithLabelValues(f)
	}
	for _, o := range []string{fetchOK, fetchCanceled, fetchError} {
		fileFetchesTotal.WithLabelValues(o)
	}
}

// archiveOutcome classifies how an archive request ended. The request
// context is consulted first: once the client is gone every later failure
// (canceled fetch, broken pipe) is a consequence, not a cause, and must not
// inflate the upstream or error series.
func archiveOutcome(ctx context.Context, err error) string {
	if err == nil {
		return outcomeOK
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return outcomeClientGone
	}
	var ue *fetch.Error
	if errors.As(err, &ue) {
		return outcomeUpstreamError
	}
	return outcomeError
}

// recordArchive closes the books on one archive request.
func recordArchive(format, outcome string, started time.Time) {
	archivesTotal.WithLabelValues(format, outcome).Inc()
	archiveDuration.WithLabelValues(format).Observe(time.Since(started).Seconds())
}

// meteredWriter counts bytes as they leave for the client rather than at
// the end of the request: a pod killed mid-stream must still have reported
// the traffic it served up to its last scrape.
type meteredWriter struct {
	w io.Writer
	c prometheus.Counter
}

func (m *meteredWriter) Write(p []byte) (int, error) {
	n, err := m.w.Write(p)
	m.c.Add(float64(n))
	return n, err
}

// meteredFetcher times every upstream GET. It sits below the Prefetcher so
// prefetches are counted too — they are real requests to the proxy.
type meteredFetcher struct {
	base fetch.Fetcher
}

func (m meteredFetcher) Fetch(ctx context.Context, url string, begin, end int64) (io.ReadCloser, error) {
	started := time.Now()
	body, err := m.base.Fetch(ctx, url, begin, end)
	switch {
	case err == nil:
		fileFetchesTotal.WithLabelValues(fetchOK).Inc()
		fileFetchDuration.Observe(time.Since(started).Seconds())
	case ctx.Err() != nil:
		// How long the client waited before leaving is not upstream
		// latency, so canceled fetches stay out of the histogram.
		fileFetchesTotal.WithLabelValues(fetchCanceled).Inc()
	default:
		// A 502 after 30 s of waiting is exactly the latency worth seeing.
		fileFetchesTotal.WithLabelValues(fetchError).Inc()
		fileFetchDuration.Observe(time.Since(started).Seconds())
	}
	return body, err
}

// newFetcher builds the fetcher chain a writer streams through: metered
// HTTP at the bottom, the per-request prefetcher on top when enabled.
func newFetcher(base fetch.Fetcher, cfg PrefetchConfig, plan []planned) fetch.Fetcher {
	var f fetch.Fetcher = meteredFetcher{base: base}
	if pf := NewPrefetcher(f, cfg, plan); pf != nil {
		f = pf
	}
	return f
}
