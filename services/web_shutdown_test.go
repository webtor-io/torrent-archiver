package services

import (
	"io"
	"net/http"
	"testing"
	"time"

	cs "github.com/webtor-io/common-services"
)

// Close drains an archive download still being written instead of cutting
// it with the listener: it returns only after the response is complete, so
// the process (which exits right after Close) does not cut the client off.
func TestWebCloseDrainsInFlightDownload(t *testing.T) {
	web := &Web{host: "127.0.0.1", port: 0, gs: cs.NewGracefulServer(5 * time.Second)}
	if err := web.Listen(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan time.Time, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Length", "12")
		_, _ = w.Write([]byte("archive-"))
		w.(http.Flusher).Flush()
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("tail"))
		finished <- time.Now()
	})
	go func() { _ = web.gs.Serve(&http.Server{Handler: mux}, web.ln) }()

	type result struct {
		body string
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + web.ln.Addr().String() + "/a.zip")
		if err != nil {
			res <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		res <- result{string(b), err}
	}()
	<-started
	web.Close()
	closed := time.Now()
	select {
	case at := <-finished:
		if closed.Before(at) {
			t.Fatal("Close returned before the in-flight download was written out")
		}
	default:
		t.Fatal("Close returned while the handler was still writing")
	}
	r := <-res
	if r.err != nil || r.body != "archive-tail" {
		t.Fatalf("in-flight download was not drained: %q %v", r.body, r.err)
	}
}
