package tarhttp_test

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webtor-io/torrent-archiver/tarhttp"
)

var testFiles = []struct {
	name string
	data string
}{
	{"dir/", ""},
	{"dir/abra", "abra"},
	{"dir/sub/", ""},
	{"dir/sub/cadabra", "cadabra"},
}

func runServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		var data []byte
		for _, f := range testFiles {
			if "/"+f.name == req.URL.Path {
				data = []byte(f.data)
			}
		}
		if req.Header.Get("Range") != "" {
			parts := strings.Split(strings.TrimPrefix(req.Header.Get("Range"), "bytes="), "-")
			begin, _ := strconv.Atoi(parts[0])
			end, _ := strconv.Atoi(parts[1])
			rw.Write(data[begin : end+1])
		} else {
			rw.Write(data)
		}
	}))
}

func getBytes(t *testing.T, s *httptest.Server, begin int64, end int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tarhttp.NewWriter(&buf, begin, end, s.Client())
	for _, f := range testFiles {
		fh := &tarhttp.FileHeader{
			Name:     f.name,
			Size:     uint64(len(f.data)),
			Modified: time.Unix(1234567890, 0),
		}
		if !strings.HasSuffix(f.name, "/") {
			fh.URL = s.URL + "/" + f.name
		}
		if err := tw.CreateHeader(context.Background(), fh); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testRead(t *testing.T, b []byte) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	i := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if i >= len(testFiles) {
			t.Fatalf("unexpected extra entry %s", hdr.Name)
		}
		want := testFiles[i]
		if strings.TrimSuffix(hdr.Name, "/") != strings.TrimSuffix(want.name, "/") {
			t.Fatalf("expected name %s got %s", want.name, hdr.Name)
		}
		var wb bytes.Buffer
		if _, err := io.Copy(&wb, tr); err != nil {
			t.Fatal(err)
		}
		if wb.String() != want.data {
			t.Fatalf("expected data %q got %q", want.data, wb.String())
		}
		i++
	}
	if i != len(testFiles) {
		t.Fatalf("expected %d entries got %d", len(testFiles), i)
	}
}

func TestWrite(t *testing.T) {
	s := runServer()
	defer s.Close()
	testRead(t, getBytes(t, s, 0, -1))
}

func TestDeterministic(t *testing.T) {
	s := runServer()
	defer s.Close()
	if !bytes.Equal(getBytes(t, s, 0, -1), getBytes(t, s, 0, -1)) {
		t.Fatal("two identical runs produced different bytes")
	}
}

func TestWriteWithOffsets(t *testing.T) {
	s := runServer()
	defer s.Close()
	full := getBytes(t, s, 0, -1)
	l := int64(len(full))
	for i := int64(0); i < l; i += 61 {
		b1 := getBytes(t, s, 0, i)
		if int64(len(b1)) != i+1 {
			t.Fatalf("offset %d: expected %d got %d", i, i+1, len(b1))
		}
		b2 := getBytes(t, s, i+1, -1)
		if int64(len(b2)) != l-i-1 {
			t.Fatalf("offset %d: expected %d got %d", i, l-i-1, len(b2))
		}
		b := append(b1, b2...)
		if !bytes.Equal(b, full) {
			t.Fatalf("offset %d: reassembled archive differs from full write", i)
		}
		testRead(t, b)
	}
}

// TestSizeAccounting mirrors how services compute Size(): a URL-less run
// plus the sum of file sizes must match the real archive length.
func TestSizeAccounting(t *testing.T) {
	s := runServer()
	defer s.Close()
	var buf bytes.Buffer
	tw := tarhttp.NewWriter(&buf, 0, -1, nil)
	var sum int64
	for _, f := range testFiles {
		fh := &tarhttp.FileHeader{
			Name:     f.name,
			Size:     uint64(len(f.data)),
			Modified: time.Unix(1234567890, 0),
		}
		if err := tw.CreateHeader(context.Background(), fh); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(f.name, "/") {
			sum += int64(len(f.data))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	size := int64(buf.Len()) + sum
	full := getBytes(t, s, 0, -1)
	if size != int64(len(full)) {
		t.Fatalf("expected size %d got %d", len(full), size)
	}
}

func TestPaxLongAndUTF8Name(t *testing.T) {
	long := strings.Repeat("d", 120) + "/файл с пробелами и кириллицей.mkv"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Write([]byte("data"))
	}))
	defer srv.Close()
	var buf bytes.Buffer
	tw := tarhttp.NewWriter(&buf, 0, -1, srv.Client())
	err := tw.CreateHeader(context.Background(), &tarhttp.FileHeader{
		Name:     long,
		URL:      srv.URL,
		Size:     4,
		Modified: time.Unix(1234567890, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != long {
		t.Fatalf("expected name %q got %q", long, hdr.Name)
	}
}

// PAX record length is self-referential; sweep name lengths across the
// digit-count boundaries of the length prefix (record base 98→99→100 at
// 91-92-byte names, 998→1000 at 991-992) where a one-pass computation
// declares a length one byte off and corrupts the archive.
func TestPaxRecordDigitBoundaries(t *testing.T) {
	for _, n := range []int{85, 89, 90, 91, 92, 93, 100, 990, 991, 992, 993} {
		name := "ф" + strings.Repeat("a", n-2) // 2-byte rune forces a PAX path record
		if len(name) != n {
			t.Fatalf("setup: len=%d want %d", len(name), n)
		}
		var buf bytes.Buffer
		tw := tarhttp.NewWriter(&buf, 0, -1, nil)
		err := tw.CreateHeader(context.Background(), &tarhttp.FileHeader{
			Name:     name,
			Modified: time.Unix(1234567890, 0),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
		hdr, err := tr.Next()
		if err != nil {
			t.Errorf("name len %d: reader error: %v", n, err)
			continue
		}
		if hdr.Name != name {
			t.Errorf("name len %d: got %q", n, hdr.Name)
		}
		if _, err := tr.Next(); err != io.EOF {
			t.Errorf("name len %d: expected clean EOF, got %v", n, err)
		}
	}
}

// Header for a file above the 8 GiB octal limit must carry a PAX size
// record that stock readers understand.
func TestPaxHugeSize(t *testing.T) {
	var size uint64 = 10 << 30
	var buf bytes.Buffer
	tw := tarhttp.NewWriter(&buf, 0, -1, nil)
	err := tw.CreateHeader(context.Background(), &tarhttp.FileHeader{
		Name:     "huge.bin",
		Size:     size,
		Modified: time.Unix(1234567890, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(hdr.Size) != size {
		t.Fatalf("expected size %d got %d", size, hdr.Size)
	}
}
