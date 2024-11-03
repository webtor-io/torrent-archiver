// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/pkg/errors"
)

var (
	errLongName  = errors.New("zip: FileHeader.Name too long")
	errLongExtra = errors.New("zip: FileHeader.Extra too long")
	errAlgorithm = errors.New("zip: unsupported compression algorithm")
)

// Writer implements a zip file writer.
type Writer struct {
	w           *bufio.Writer
	dir         []*header
	closed      bool
	compressors map[uint16]Compressor
	comment     string
	begin       int64
	end         int64
	current     int64
	cl          *http.Client

	// testHookCloseSizeOffset if non-nil is called with the size
	// of offset of the central directory at Close.
	testHookCloseSizeOffset func(size, offset uint64)
}

type header struct {
	*FileHeader
	offset uint64
}

// NewWriter returns a new Writer writing a zip file to w.
func NewWriter(w io.Writer, begin int64, end int64, cl *http.Client) *Writer {
	if cl == nil {
		cl = http.DefaultClient
	}
	return &Writer{begin: begin, end: end, cl: cl, w: bufio.NewWriter(w)}
}

// Flush flushes any buffered data to the underlying writer.
// Calling Flush is not normally necessary; calling Close is sufficient.
func (w *Writer) Flush() error {
	return w.w.Flush()
}

// SetComment sets the end-of-central-directory comment field.
// It can only be called before Close.
func (w *Writer) SetComment(comment string) error {
	if len(comment) > uint16max {
		return errors.New("zip: Writer.Comment too long")
	}
	w.comment = comment
	return nil
}

func (w *Writer) getEnding() ([]byte, error) {

	var bb bytes.Buffer

	cw := &countWriter{
		w:     &bb,
		count: w.current,
	}
	// write central directory
	start := cw.count
	for _, h := range w.dir {
		var buf [directoryHeaderLen]byte
		b := writeBuf(buf[:])
		b.uint32(uint32(directoryHeaderSignature))
		b.uint16(h.CreatorVersion)
		b.uint16(h.ReaderVersion)
		b.uint16(h.Flags)
		b.uint16(h.Method)
		b.uint16(h.ModifiedTime)
		b.uint16(h.ModifiedDate)
		b.uint32(h.CRC32)
		if h.isZip64() || h.offset >= uint32max {
			// the file needs a zip64 header. store maxint in both
			// 32 bit size fields (and offset later) to signal that the
			// zip64 extra header should be used.
			b.uint32(uint32max) // compressed size
			b.uint32(uint32max) // uncompressed size

			// append a zip64 extra block to Extra
			var buf [28]byte // 2x uint16 + 3x uint64
			eb := writeBuf(buf[:])
			eb.uint16(zip64ExtraID)
			eb.uint16(24) // size = 3x uint64
			eb.uint64(h.UncompressedSize64)
			eb.uint64(h.CompressedSize64)
			eb.uint64(h.offset)
			h.Extra = append(h.Extra, buf[:]...)
		} else {
			b.uint32(h.CompressedSize)
			b.uint32(h.UncompressedSize)
		}
		b.uint16(uint16(len(h.Name)))
		b.uint16(uint16(len(h.Extra)))
		b.uint16(uint16(len(h.Comment)))
		b = b[4:] // skip disk number start and internal file attr (2x uint16)
		b.uint32(h.ExternalAttrs)
		if h.offset > uint32max {
			b.uint32(uint32max)
		} else {
			b.uint32(uint32(h.offset))
		}
		if _, err := cw.Write(buf[:]); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(cw, h.Name); err != nil {
			return nil, err
		}
		if _, err := cw.Write(h.Extra); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(cw, h.Comment); err != nil {
			return nil, err
		}
	}
	end := cw.count

	records := uint64(len(w.dir))
	size := uint64(end - start)
	offset := uint64(start)

	if f := w.testHookCloseSizeOffset; f != nil {
		f(size, offset)
	}

	if records >= uint16max || size >= uint32max || offset >= uint32max {
		var buf [directory64EndLen + directory64LocLen]byte
		b := writeBuf(buf[:])

		// zip64 end of central directory record
		b.uint32(directory64EndSignature)
		b.uint64(directory64EndLen - 12) // length minus signature (uint32) and length fields (uint64)
		b.uint16(zipVersion45)           // version made by
		b.uint16(zipVersion45)           // version needed to extract
		b.uint32(0)                      // number of this disk
		b.uint32(0)                      // number of the disk with the start of the central directory
		b.uint64(records)                // total number of entries in the central directory on this disk
		b.uint64(records)                // total number of entries in the central directory
		b.uint64(size)                   // size of the central directory
		b.uint64(offset)                 // offset of start of central directory with respect to the starting disk number

		// zip64 end of central directory locator
		b.uint32(directory64LocSignature)
		b.uint32(0)           // number of the disk with the start of the zip64 end of central directory
		b.uint64(uint64(end)) // relative offset of the zip64 end of central directory record
		b.uint32(1)           // total number of disks

		if _, err := cw.Write(buf[:]); err != nil {
			return nil, err
		}

		// store max values in the regular end record to signal
		// that the zip64 values should be used instead
		records = uint16max
		size = uint32max
		offset = uint32max
	}

	// write end record
	var buf [directoryEndLen]byte
	b := writeBuf(buf[:])
	b.uint32(uint32(directoryEndSignature))
	b = b[4:]                        // skip over disk number and first disk number (2x uint16)
	b.uint16(uint16(records))        // number of entries this disk
	b.uint16(uint16(records))        // number of entries total
	b.uint32(uint32(size))           // size of directory
	b.uint32(uint32(offset))         // start of directory
	b.uint16(uint16(len(w.comment))) // byte size of EOCD comment
	if _, err := cw.Write(buf[:]); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(cw, w.comment); err != nil {
		return nil, err
	}
	return bb.Bytes(), nil
}

// Close finishes writing the zip file by writing the central directory.
// It does not close the underlying writer.
func (w *Writer) Close() error {
	if w.closed {
		return errors.New("zip: writer closed twice")
	}
	w.closed = true

	bytes, err := w.getEnding()
	if err != nil {
		return err
	}

	bl := int64(len(bytes))
	_, skip, begin, end := w.getRange(bl)
	if skip {
		return w.w.Flush()
	}
	_, err = w.w.Write(bytes[begin:end])
	if err != nil {
		return err
	}

	return w.w.Flush()
}

// detectUTF8 reports whether s is a valid UTF-8 string, and whether the string
// must be considered UTF-8 encoding (i.e., not compatible with CP-437, ASCII,
// or any other common encoding).
func detectUTF8(s string) (valid, require bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		// Officially, ZIP uses CP-437, but many readers use the system's
		// local character encoding. Most encoding are compatible with a large
		// subset of CP-437, which itself is ASCII-like.
		//
		// Forbid 0x7e and 0x5c since EUC-KR and Shift-JIS replace those
		// characters with localized currency and overline characters.
		if r < 0x20 || r > 0x7d || r == 0x5c {
			if !utf8.ValidRune(r) || (r == utf8.RuneError && size == 1) {
				return false, false
			}
			require = true
		}
	}
	return true, require
}

// CreateHeader adds a file to the zip archive using the provided FileHeader
// for the file metadata. Writer takes ownership of fh and may mutate
// its fields. The caller must not modify fh after calling CreateHeader.
//
// This returns a Writer to which the file contents should be written.
// The file's contents must be written to the io.Writer before the next
// call to Create, CreateHeader, or Close.
func (w *Writer) CreateHeader(ctx context.Context, fh *FileHeader) error {
	if len(w.dir) > 0 && w.dir[len(w.dir)-1].FileHeader == fh {
		// See https://golang.org/issue/11144 confusion.
		return errors.New("archive/zip: invalid duplicate FileHeader")
	}

	// The ZIP format has a sad state of affairs regarding character encoding.
	// Officially, the name and comment fields are supposed to be encoded
	// in CP-437 (which is mostly compatible with ASCII), unless the UTF-8
	// flag bit is set. However, there are several problems:
	//
	//	* Many ZIP readers still do not support UTF-8.
	//	* If the UTF-8 flag is cleared, several readers simply interpret the
	//	name and comment fields as whatever the local system encoding is.
	//
	// In order to avoid breaking readers without UTF-8 support,
	// we avoid setting the UTF-8 flag if the strings are CP-437 compatible.
	// However, if the strings require multibyte UTF-8 encoding and is a
	// valid UTF-8 string, then we set the UTF-8 bit.
	//
	// For the case, where the user explicitly wants to specify the encoding
	// as UTF-8, they will need to set the flag bit themselves.
	utf8Valid1, utf8Require1 := detectUTF8(fh.Name)
	utf8Valid2, utf8Require2 := detectUTF8(fh.Comment)
	switch {
	case fh.NonUTF8:
		fh.Flags &^= 0x800
	case (utf8Require1 || utf8Require2) && (utf8Valid1 && utf8Valid2):
		fh.Flags |= 0x800
	}

	fh.CreatorVersion = fh.CreatorVersion&0xff00 | zipVersion20 // preserve compatibility byte
	fh.ReaderVersion = zipVersion20

	// If Modified is set, this takes precedence over MS-DOS timestamp fields.
	if !fh.Modified.IsZero() {
		// Contrary to the FileHeader.SetModTime method, we intentionally
		// do not convert to UTC, because we assume the user intends to encode
		// the date using the specified timezone. A user may want this control
		// because many legacy ZIP readers interpret the timestamp according
		// to the local timezone.
		//
		// The timezone is only non-UTC if a user directly sets the Modified
		// field directly themselves. All other approaches sets UTC.
		fh.ModifiedDate, fh.ModifiedTime = timeToMsDosTime(fh.Modified)

		// Use "extended timestamp" format since this is what Info-ZIP uses.
		// Nearly every major ZIP implementation uses a different format,
		// but at least most seem to be able to understand the other formats.
		//
		// This format happens to be identical for both local and central header
		// if modification time is the only timestamp being encoded.
		var mbuf [9]byte // 2*SizeOf(uint16) + SizeOf(uint8) + SizeOf(uint32)
		mt := uint32(fh.Modified.Unix())
		eb := writeBuf(mbuf[:])
		eb.uint16(extTimeExtraID)
		eb.uint16(5)  // Size: SizeOf(uint8) + SizeOf(uint32)
		eb.uint8(1)   // Flags: ModTime
		eb.uint32(mt) // ModTime
		fh.Extra = append(fh.Extra, mbuf[:]...)
	}

	fh.CompressedSize64 = fh.UncompressedSize64

	if fh.isZip64() {
		fh.CompressedSize = uint32max
		fh.UncompressedSize = uint32max
		fh.ReaderVersion = zipVersion45 // requires 4.5 - File uses ZIP64 format extensions
	} else {
		fh.CompressedSize = uint32(fh.CompressedSize64)
		fh.UncompressedSize = uint32(fh.UncompressedSize64)
	}

	var (
		fw io.Writer
	)
	h := &header{
		FileHeader: fh,
		offset:     uint64(w.current),
	}

	fh.Method = Store

	if strings.HasSuffix(fh.Name, "/") {

		fh.Flags &^= 0x8 // we will not write a data descriptor

		// Set the compression method to Store to ensure data length is truly zero,
		// which the writeHeader method always encodes for the size fields.
		// This is necessary as most compression formats have non-zero lengths
		// even when compressing an empty string.

		// Explicitly clear sizes as they have no meaning for directories.
		fh.CompressedSize = 0
		fh.CompressedSize64 = 0
		fh.UncompressedSize = 0
		fh.UncompressedSize64 = 0
	} else {

		fh.Flags |= 0x8 // we will write a data descriptor

		comp := w.compressor(fh.Method)
		if comp == nil {
			return errAlgorithm
		}
		var err error
		fw, err = comp(w.w)
		if err != nil {
			return err
		}
	}
	w.dir = append(w.dir, h)
	if err := w.writeHeader(fh); err != nil {
		return err
	}
	// If we're creating a directory, fw is nil.
	if h.URL != "" {
		cw := &crcWriter{
			w:     fw,
			crc32: crc32.NewIEEE(),
		}
		err := w.writeFile(ctx, fh, cw)
		if err != nil {
			return err
		}
		h.CRC32 = cw.crc32.Sum32()
	}
	w.current += int64(h.UncompressedSize64)
	if fh.hasDataDescriptor() {
		err := w.writeDescriptor(fh, fw)
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) getRange(l int64) (partial bool, skip bool, begin int64, end int64) {
	end = l
	if w.current+l <= w.begin || (w.current > w.end && w.end != -1) {
		partial = true
		skip = true
		return
	}
	if w.current+l > w.begin && w.current < w.begin {
		partial = true
		begin = w.begin - w.current
	}
	if w.current+l > w.end && w.current <= w.end {
		partial = true
		end = w.end - w.current + 1
	}
	return
}

func (w *Writer) getDescriptor(h *FileHeader) ([]byte, error) {
	var wb bytes.Buffer
	// Write data descriptor. This is more complicated than one would
	// think, see e.g. comments in zipfile.c:putextended() and
	// http://bugs.sun.com/bugdatabase/view_bug.do?bug_id=7073588.
	// The approach here is to write 8 byte sizes if needed without
	// adding a zip64 extra in the local header (too late anyway).
	var buf []byte
	if h.isZip64() {
		buf = make([]byte, dataDescriptor64Len)
	} else {
		buf = make([]byte, dataDescriptorLen)
	}
	b := writeBuf(buf)
	b.uint32(dataDescriptorSignature) // de-facto standard, required by OS X
	b.uint32(0)
	if h.isZip64() {
		b.uint64(h.CompressedSize64)
		b.uint64(h.UncompressedSize64)
	} else {
		b.uint32(h.CompressedSize)
		b.uint32(h.UncompressedSize)
	}
	_, err := wb.Write(buf)
	if err != nil {
		return nil, err
	}
	return wb.Bytes(), nil
}

func (w *Writer) writeDescriptor(h *FileHeader, fw io.Writer) error {
	bytes, err := w.getDescriptor(h)
	if err != nil {
		return err
	}
	bl := int64(len(bytes))
	_, skip, begin, end := w.getRange(bl)
	w.current += bl
	if skip {
		return nil
	}
	_, err = w.w.Write(bytes[begin:end])
	return err
}

type crcWriter struct {
	w     io.Writer
	crc32 hash.Hash32
}

func (w *crcWriter) Write(p []byte) (int, error) {
	w.crc32.Write(p)
	return w.w.Write(p)
}

func (w *Writer) writeFile(ctx context.Context, h *FileHeader, fw io.Writer) error {
	partial, skip, begin, end := w.getRange(int64(h.UncompressedSize64))
	if skip {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", h.URL, nil)
	if err != nil {
		return err
	}
	if partial {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", begin, end-1))
	}
	res, err := w.cl.Do(req)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return errors.Errorf("got bad http code from url=%v code=%v", h.URL, res.StatusCode)
	}
	defer res.Body.Close()
	// n, err := io.Copy(fw, res.Body)
	// fmt.Printf("Write content for file=%v len=%v url=%v begin=%v end=%v size=%v\n", h.Name, h.UncompressedSize64, h.URL, begin, end, n)
	_, err = io.Copy(fw, res.Body)
	if err != nil {
		return err
	}
	return nil
}
func (w *Writer) writeHeader(h *FileHeader) error {
	bytes, err := w.getHeader(h)
	if err != nil {
		return err
	}
	bl := int64(len(bytes))
	_, skip, begin, end := w.getRange(bl)
	w.current += bl
	if skip {
		return nil
	}
	_, err = w.w.Write(bytes[begin:end])
	// n, err := w.cw.Write(bytes[begin:end])
	// fmt.Printf("Write header for file=%v len=%v begin=%v end=%v size=%v\n", h.Name, h.Length, begin, end, n)
	return err
}

func (w *Writer) getHeader(h *FileHeader) ([]byte, error) {
	var wb bytes.Buffer
	const maxUint16 = 1<<16 - 1
	if len(h.Name) > maxUint16 {
		return nil, errLongName
	}
	if len(h.Extra) > maxUint16 {
		return nil, errLongExtra
	}

	var buf [fileHeaderLen]byte
	b := writeBuf(buf[:])
	b.uint32(uint32(fileHeaderSignature))
	b.uint16(h.ReaderVersion)
	b.uint16(h.Flags)
	b.uint16(h.Method)
	b.uint16(h.ModifiedTime)
	b.uint16(h.ModifiedDate)
	// since we are writing a data descriptor crc32,
	b.uint32(0)
	b.uint32(0)
	b.uint32(0)

	b.uint16(uint16(len(h.Name)))
	b.uint16(uint16(len(h.Extra)))
	if _, err := wb.Write(buf[:]); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(&wb, h.Name); err != nil {
		return nil, err
	}
	if _, err := wb.Write(h.Extra); err != nil {
		return nil, err
	}
	return wb.Bytes(), nil
}

// RegisterCompressor registers or overrides a custom compressor for a specific
// method ID. If a compressor for a given method is not found, Writer will
// default to looking up the compressor at the package level.
func (w *Writer) RegisterCompressor(method uint16, comp Compressor) {
	if w.compressors == nil {
		w.compressors = make(map[uint16]Compressor)
	}
	w.compressors[method] = comp
}

func (w *Writer) compressor(method uint16) Compressor {
	comp := w.compressors[method]
	if comp == nil {
		comp = compressor(method)
	}
	return comp
}

type dirWriter struct{}

func (dirWriter) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	return 0, errors.New("zip: write to directory")
}

type countWriter struct {
	w     io.Writer
	count int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.count += int64(n)
	return n, err
}

type nopCloser struct {
	io.Writer
}

func (w nopCloser) Close() error {
	return nil
}

type writeBuf []byte

func (b *writeBuf) uint8(v uint8) {
	(*b)[0] = v
	*b = (*b)[1:]
}

func (b *writeBuf) uint16(v uint16) {
	binary.LittleEndian.PutUint16(*b, v)
	*b = (*b)[2:]
}

func (b *writeBuf) uint32(v uint32) {
	binary.LittleEndian.PutUint32(*b, v)
	*b = (*b)[4:]
}

func (b *writeBuf) uint64(v uint64) {
	binary.LittleEndian.PutUint64(*b, v)
	*b = (*b)[8:]
}
