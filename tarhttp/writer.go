// Package tarhttp writes a deterministic ustar/PAX tar stream where file
// contents are fetched over HTTP. Because the archive layout is a pure
// function of the file list (names, sizes, mtimes), the total size can be
// computed without touching any content and any byte range of the archive
// can be regenerated independently — the writer skips everything outside
// [begin, end] and fetches only the overlapping content ranges upstream.
// This mirrors the range semantics of the ziphttp package.
package tarhttp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/webtor-io/torrent-archiver/internal/fetch"
	"github.com/webtor-io/torrent-archiver/internal/rangeclip"
)

const blockSize = 512

// FileHeader describes a single archive entry. A trailing slash in Name
// marks a directory. URL may be empty for size-only runs: the writer then
// accounts for the content bytes without emitting them.
type FileHeader struct {
	Name     string
	URL      string
	Size     uint64
	Modified time.Time
}

// Writer emits the byte range [begin, end] (inclusive; end == -1 means
// unbounded) of the deterministic tar stream.
type Writer struct {
	w       *bufio.Writer
	begin   int64
	end     int64
	current int64
	fetcher fetch.Fetcher
	closed  bool
}

func NewWriter(w io.Writer, begin int64, end int64, cl *http.Client) *Writer {
	return &Writer{begin: begin, end: end, fetcher: fetch.HTTP{Client: cl}, w: bufio.NewWriter(w)}
}

// SetFetcher replaces the plain HTTP fetcher — the archiver installs a
// prefetching one so upcoming small files are already in memory when the
// sequential stream reaches them.
func (w *Writer) SetFetcher(f fetch.Fetcher) {
	if f != nil {
		w.fetcher = f
	}
}

// getRange clips a chunk of length l at the current logical offset against
// the requested [begin, end] window. Same semantics as ziphttp.
func (w *Writer) getRange(l int64) (partial bool, skip bool, begin int64, end int64) {
	return rangeclip.Clip(w.current, w.begin, w.end, l)
}

// writeBytes emits the part of b that falls inside the requested window and
// advances the logical offset by the full length of b.
func (w *Writer) writeBytes(b []byte) error {
	l := int64(len(b))
	_, skip, begin, end := w.getRange(l)
	w.current += l
	if skip {
		return nil
	}
	_, err := w.w.Write(b[begin:end])
	return err
}

// CreateHeader writes the header block(s) for fh (including a PAX extension
// entry when the name or size does not fit ustar limits), then the content
// fetched from fh.URL, then zero padding up to the 512-byte boundary.
func (w *Writer) CreateHeader(ctx context.Context, fh *FileHeader) error {
	if w.closed {
		return errors.New("tar: writer closed")
	}
	blocks, err := encodeHeader(fh)
	if err != nil {
		return err
	}
	if err := w.writeBytes(blocks); err != nil {
		return err
	}
	if strings.HasSuffix(fh.Name, "/") {
		return nil
	}
	if err := w.writeContent(ctx, fh); err != nil {
		return err
	}
	if pad := padding(int64(fh.Size)); pad > 0 {
		return w.writeBytes(make([]byte, pad))
	}
	return nil
}

func (w *Writer) writeContent(ctx context.Context, fh *FileHeader) error {
	size := int64(fh.Size)
	partial, skip, begin, end := w.getRange(size)
	if skip || fh.URL == "" {
		w.current += size
		return nil
	}
	defer func() {
		w.current += size
	}()
	// Everything written so far (this entry's header included) reaches the
	// client before we block on upstream: a slow swarm must not look like a
	// silent connection — download managers give up on 30 s of silence.
	if err := w.w.Flush(); err != nil {
		return err
	}
	// Whole file → no Range header (begin 0, end -1); a window cut on
	// either side → an explicit closed range, exactly as before the seam.
	if !partial {
		end = -1
	}
	body, err := w.fetcher.Fetch(ctx, fh.URL, begin, end)
	if err != nil {
		return err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(body)
	_, err = io.Copy(w.w, body)
	return err
}

// Close writes the end-of-archive marker (two zero blocks). It does not
// close the underlying writer.
func (w *Writer) Close() error {
	if w.closed {
		return errors.New("tar: writer closed twice")
	}
	w.closed = true
	if err := w.writeBytes(make([]byte, 2*blockSize)); err != nil {
		return err
	}
	return w.w.Flush()
}

func padding(size int64) int64 {
	if r := size % blockSize; r != 0 {
		return blockSize - r
	}
	return 0
}

const (
	// Largest value that fits a 12-byte octal ustar numeric field
	// (11 octal digits): 8 GiB - 1.
	maxOctalSize = 1<<33 - 1
	maxNameLen   = 100
)

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// encodeHeader serializes the ustar header block for fh, preceded by a PAX
// extended header entry when the name is too long / non-ASCII or the size
// exceeds the octal field limit. The output length is a multiple of 512 and
// depends only on fh fields — never on wall clock or process state.
func encodeHeader(fh *FileHeader) ([]byte, error) {
	isDir := strings.HasSuffix(fh.Name, "/")
	var mode int64 = 0644
	typeflag := byte('0')
	if isDir {
		mode = 0755
		typeflag = '5'
	}
	size := int64(fh.Size)
	if isDir {
		size = 0
	}
	mtime := fh.Modified.Unix()
	if mtime < 0 || mtime > maxOctalSize {
		mtime = 0
	}

	var paxRecords []string
	if len(fh.Name) > maxNameLen || !isASCII(fh.Name) {
		paxRecords = append(paxRecords, paxRecord("path", fh.Name))
	}
	fieldSize := size
	if size > maxOctalSize {
		paxRecords = append(paxRecords, paxRecord("size", strconv.FormatInt(size, 10)))
		fieldSize = 0
	}

	var out bytes.Buffer
	if len(paxRecords) > 0 {
		data := []byte(strings.Join(paxRecords, ""))
		paxName := paxHeaderName(fh.Name)
		out.Write(ustarBlock(paxName, 0644, int64(len(data)), mtime, 'x'))
		out.Write(data)
		if pad := padding(int64(len(data))); pad > 0 {
			out.Write(make([]byte, pad))
		}
	}
	out.Write(ustarBlock(truncateName(fh.Name), mode, fieldSize, mtime, typeflag))
	return out.Bytes(), nil
}

// paxRecord formats a single "%d keyword=value\n" record where the leading
// decimal length covers the whole record including itself. The length is
// self-referential (more digits → longer record → possibly more digits), so
// iterate to a fixed point; it converges in at most two extra rounds.
func paxRecord(keyword, value string) string {
	const padLen = 3 // ' ', '=', '\n'
	size := len(keyword) + len(value) + padLen
	size += len(strconv.Itoa(size))
	for {
		record := strconv.Itoa(size) + " " + keyword + "=" + value + "\n"
		if len(record) == size {
			return record
		}
		size = len(record)
	}
}

// paxHeaderName builds the name field of the PAX extension entry. Readers
// take the real name from the "path" record, so this only has to be a
// deterministic ustar-safe placeholder.
func paxHeaderName(name string) string {
	name = strings.TrimSuffix(name, "/")
	return truncateName("PaxHeaders.0/" + name)
}

// truncateName clips a name to the 100-byte ustar field without splitting a
// UTF-8 sequence, preserving a trailing slash so directories stay marked.
func truncateName(name string) string {
	if len(name) <= maxNameLen {
		return name
	}
	isDir := strings.HasSuffix(name, "/")
	max := maxNameLen
	if isDir {
		max--
	}
	for max > 0 && name[max]&0xc0 == 0x80 {
		max--
	}
	name = name[:max]
	if isDir {
		name += "/"
	}
	return name
}

func ustarBlock(name string, mode int64, size int64, mtime int64, typeflag byte) []byte {
	b := make([]byte, blockSize)
	copy(b[0:100], name)
	octal(b[100:108], mode)
	octal(b[108:116], 0) // uid
	octal(b[116:124], 0) // gid
	octal(b[124:136], size)
	octal(b[136:148], mtime)
	b[156] = typeflag
	copy(b[257:263], "ustar\x00")
	copy(b[263:265], "00")
	// checksum: unsigned sum of the block with the checksum field blanked
	for i := 148; i < 156; i++ {
		b[i] = ' '
	}
	var sum int64
	for _, c := range b {
		sum += int64(c)
	}
	s := fmt.Sprintf("%06o", sum)
	copy(b[148:154], s)
	b[154] = 0
	b[155] = ' '
	return b
}

// octal writes v right-justified with leading zeros and a trailing NUL.
func octal(b []byte, v int64) {
	s := strconv.FormatInt(v, 8)
	for len(s) < len(b)-1 {
		s = "0" + s
	}
	copy(b, s)
	b[len(b)-1] = 0
}
