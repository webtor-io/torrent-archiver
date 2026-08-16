package ziphttp_test

import (
	"bytes"
	"context"
	"hash/crc32"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/webtor-io/torrent-archiver/ziphttp"
)

// fakeResumer is an in-memory ziphttp.CRCResumer.
type fakeResumer struct {
	mux   sync.Mutex
	crcs  map[string]uint32
	ckpts map[string]map[int64]uint32
}

func newFakeResumer() *fakeResumer {
	return &fakeResumer{crcs: map[string]uint32{}, ckpts: map[string]map[int64]uint32{}}
}

func (f *fakeResumer) PutCRC(_ context.Context, key string, crc uint32) {
	f.mux.Lock()
	defer f.mux.Unlock()
	f.crcs[key] = crc
}

func (f *fakeResumer) GetCheckpoint(_ context.Context, key string, limit int64) (offset int64, state uint32, ok bool) {
	f.mux.Lock()
	defer f.mux.Unlock()
	for o, s := range f.ckpts[key] {
		if o <= limit && (!ok || o > offset) {
			offset, state, ok = o, s, true
		}
	}
	return
}

func (f *fakeResumer) PutCheckpoint(_ context.Context, key string, offset int64, state uint32) {
	f.mux.Lock()
	defer f.mux.Unlock()
	if f.ckpts[key] == nil {
		f.ckpts[key] = map[int64]uint32{}
	}
	f.ckpts[key][offset] = state
}

// crcTestFiles are large enough for a resume point to land mid-file with
// several checkpoint intervals behind it.
func crcTestFiles() map[string][]byte {
	rnd := rand.New(rand.NewSource(42))
	files := map[string][]byte{}
	for _, name := range []string{"file1", "file2"} {
		b := make([]byte, 100_000)
		rnd.Read(b)
		files[name] = b
	}
	return files
}

var crcTestOrder = []string{"file1", "file2"}

// rangeLog records every upstream fetch as (path, begin, length).
type rangeLog struct {
	mux  sync.Mutex
	reqs [][3]int64
	name map[int64]string
}

func runCRCServer(files map[string][]byte) (*httptest.Server, *rangeLog) {
	rl := &rangeLog{name: map[int64]string{}}
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		name := strings.TrimPrefix(req.URL.Path, "/")
		data := files[name]
		begin, end := 0, len(data)-1
		if rng := req.Header.Get("Range"); rng != "" {
			parts := strings.Split(strings.TrimPrefix(rng, "bytes="), "-")
			begin, _ = strconv.Atoi(parts[0])
			end, _ = strconv.Atoi(parts[1])
		}
		rl.mux.Lock()
		id := int64(len(rl.reqs))
		rl.reqs = append(rl.reqs, [3]int64{id, int64(begin), int64(end - begin + 1)})
		rl.name[id] = name
		rl.mux.Unlock()
		_, _ = rw.Write(data[begin : end+1])
	})), rl
}

func (rl *rangeLog) reset() {
	rl.mux.Lock()
	defer rl.mux.Unlock()
	rl.reqs = nil
	rl.name = map[int64]string{}
}

// fetches returns each recorded (begin, length) fetch of the named file.
func (rl *rangeLog) fetches(name string) [][2]int64 {
	rl.mux.Lock()
	defer rl.mux.Unlock()
	var res [][2]int64
	for _, r := range rl.reqs {
		if rl.name[r[0]] == name {
			res = append(res, [2]int64{r[1], r[2]})
		}
	}
	return res
}

// writeCRCZip emulates the services layer: CRCKey per file plus central
// directory pre-seeding from the resumer's full-CRC knowledge.
func writeCRCZip(t *testing.T, s *httptest.Server, files map[string][]byte, begin int64, end int64, crcs ziphttp.CRCResumer, preseed map[string]uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := ziphttp.NewWriter(&buf, begin, end, s.Client(), crcs)
	zw.SetCRCCheckpointInterval(10_000)
	for _, name := range crcTestOrder {
		header := &ziphttp.FileHeader{
			Name:               name,
			URL:                s.URL + "/" + name,
			UncompressedSize64: uint64(len(files[name])),
			CRCKey:             name,
			CRC32:              preseed[name],
		}
		header.SetMode(os.FileMode(int(0644)))
		if err := zw.CreateHeader(context.Background(), header); err != nil {
			t.Fatalf("CreateHeader(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func readCentralCRCs(t *testing.T, b []byte) map[string]uint32 {
	t.Helper()
	r, err := ziphttp.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	res := map[string]uint32{}
	for _, f := range r.File {
		res[f.Name] = f.CRC32
	}
	return res
}

// TestResumeSpliceWithCRCStore is the Safari-resume scenario: request A
// streams a prefix and dies mid-file2, request B (sharing the CRC store)
// serves the rest. The spliced bytes must be identical to an uninterrupted
// full download — real checksums included.
func TestResumeSpliceWithCRCStore(t *testing.T) {
	files := crcTestFiles()
	s, rlog := runCRCServer(files)
	defer s.Close()

	full := writeCRCZip(t, s, files, 0, -1, newFakeResumer(), nil)

	// Server A streamed up to x (well inside file2's data region), but the
	// client durably kept only up to y: the in-flight tail was lost with
	// the connection — the realistic Safari resume shape. Both offsets sit
	// mid-file2, off checkpoint boundaries, several intervals in.
	x := int64(len(full)) - 60_000
	y := x - 7_001

	rs := newFakeResumer()
	a := writeCRCZip(t, s, files, 0, x, rs, nil)
	rlog.reset()
	b := writeCRCZip(t, s, files, y+1, -1, rs, rs.crcs)

	// The resume must re-anchor from A's checkpoint, not by re-reading the
	// straddling file from byte 0: no zero-offset fetch, and the gap read
	// stays within one checkpoint interval.
	var sawGapRead bool
	for _, fr := range rlog.fetches("file2") {
		if fr[0] == 0 {
			t.Fatalf("resume re-read file2 prefix from offset 0 (len %d) — checkpoint unused", fr[1])
		}
		if fr[1] <= 10_000 {
			sawGapRead = true
		}
	}
	if !sawGapRead {
		t.Fatal("no bounded gap read observed — expected a short checkpoint top-up fetch")
	}

	splice := append(append([]byte{}, a[:y+1]...), b...)
	if !bytes.Equal(splice, full) {
		t.Fatalf("resumed splice differs from uninterrupted download (len %d vs %d)", len(splice), len(full))
	}
	crcs := readCentralCRCs(t, splice)
	for _, name := range crcTestOrder {
		want := crc32.ChecksumIEEE(files[name])
		if crcs[name] != want {
			t.Fatalf("central CRC for %s = %#x, want %#x", name, crcs[name], want)
		}
	}
}

// TestResumeColdCache resumes with an empty store: the straddling file is
// re-anchored by the bounded upstream gap read, while the fully-skipped
// file1 must degrade to exactly 0 — never a partially-computed value.
func TestResumeColdCache(t *testing.T) {
	files := crcTestFiles()
	s, _ := runCRCServer(files)
	defer s.Close()

	full := writeCRCZip(t, s, files, 0, -1, newFakeResumer(), nil)
	x := int64(len(full)) / 2

	rs := newFakeResumer()
	b := writeCRCZip(t, s, files, x+1, -1, rs, nil)
	splice := append(append([]byte{}, full[:x+1]...), b...)

	crcs := readCentralCRCs(t, splice)
	if want := crc32.ChecksumIEEE(files["file2"]); crcs["file2"] != want {
		t.Fatalf("straddling file2 CRC = %#x, want %#x (gap re-anchor failed)", crcs["file2"], want)
	}
	if crcs["file1"] != 0 {
		t.Fatalf("skipped file1 CRC = %#x, want exactly 0", crcs["file1"])
	}
}

// TestResumeWithoutResumer is the negative control for the store: without
// one, a file lying fully before the resumed window must emit exactly 0
// (the pre-fix code emitted partially-computed garbage in resumed windows),
// while the straddling file is still re-anchored by the bounded gap read —
// that path needs no store, only an upstream within MaxCRCGapFetch.
func TestResumeWithoutResumer(t *testing.T) {
	files := crcTestFiles()
	s, _ := runCRCServer(files)
	defer s.Close()

	full := writeCRCZip(t, s, files, 0, -1, nil, nil)
	x := int64(len(full)) / 2
	b := writeCRCZip(t, s, files, x+1, -1, nil, nil)
	splice := append(append([]byte{}, full[:x+1]...), b...)

	if bytes.Equal(splice, full) {
		t.Fatal("splice unexpectedly equals full download — the CRC-store test would be vacuous")
	}
	crcs := readCentralCRCs(t, splice)
	if want := crc32.ChecksumIEEE(files["file2"]); crcs["file2"] != want {
		t.Fatalf("straddling file2 CRC = %#x, want %#x (storeless gap re-anchor)", crcs["file2"], want)
	}
	if crcs["file1"] != 0 {
		t.Fatalf("skipped file1 CRC = %#x, want exactly 0 without a store", crcs["file1"])
	}
}
