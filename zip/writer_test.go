package zip_test

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/webtor-io/torrent-archiver/zip"
)

var (
	data = []string{"abra", "cadabra"}
	l    = int64(207)
)

func getLen(s *httptest.Server, begin int64, end int64, data []string) int64 {
	return l
	// var buf bytes.Buffer
	// zw := zip.NewWriter(&buf, begin, end, s.Client())
	// var l int64
	// for _, d := range data {
	// 	zw.CreateHeader(&zip.FileHeader{
	// 		Name: d,
	// 	})
	// 	l += int64(len(d))
	// }
	// zw.Close()
	// l += int64(buf.Len())
	// return l
}

func getBytes(s *httptest.Server, begin int64, end int64, data []string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf, begin, end, s.Client())
	for _, d := range data {
		zw.CreateHeader(&zip.FileHeader{
			Name:   d,
			URL:    s.URL + "/" + d,
			Length: int64(len(d)),
		})
	}
	zw.Close()
	return buf.Bytes()
}

func testRead(t *testing.T, b []byte, data []string) {
	var wb bytes.Buffer
	r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(&wb, rc)
	}
	if string(wb.Bytes()) != strings.Join(data, "") {
		t.Fatalf("Expected %s got %s", strings.Join(data, ""), wb.Bytes())
	}
}

func testOffset(t *testing.T, s *httptest.Server, i int64, data []string, l int64) {
	b1 := getBytes(s, 0, i, data)
	if i+1 != int64(len(b1)) {
		t.Fatalf("Expected %d got %d", i+1, len(b1))
	}
	b2 := getBytes(s, i+1, -1, data)
	if l-i-1 != int64(len(b2)) {
		t.Fatalf("Expected %d got %d", l-i-1, len(b2))
	}
	b := append(b1, b2...)
	if l != int64(len(b)) {
		t.Fatalf("Expected %d got %d", l, len(b))
	}
	testRead(t, b, data)
}

func runServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		data := []byte(strings.TrimPrefix(req.URL.String(), "/"))
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

func TestWrite(t *testing.T) {
	s := runServer()
	defer s.Close()
	l := getLen(s, 0, -1, data)
	b := getBytes(s, 0, -1, data)
	if l != int64(len(b)) {
		t.Fatalf("Expected %d got %d", l, len(b))
	}
	testRead(t, b, data)
}

func TestWriteWithOffsets(t *testing.T) {
	s := runServer()
	defer s.Close()
	l := getLen(s, 0, -1, data)
	for i := int64(0); i < l; i++ {
		testOffset(t, s, i, data, l)
	}
}

func TestWriteWithOffset33(t *testing.T) {
	s := runServer()
	defer s.Close()
	l := getLen(s, 0, -1, data)
	testOffset(t, s, 33, data, l)
}

func TestWriteWithOffset34(t *testing.T) {
	s := runServer()
	defer s.Close()
	l := getLen(s, 0, -1, data)
	testOffset(t, s, 34, data, l)
}
