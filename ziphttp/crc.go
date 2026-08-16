package ziphttp

import (
	"context"
	"hash/crc32"
	"io"
)

// CRCResumer persists content-derived CRC32 knowledge between requests:
// full-file checksums and mid-file running states (checkpoints). Both are
// pure functions of the torrent content (keyed by FileHeader.CRCKey inside
// an implementation-chosen scope, e.g. infohash), so values may be shared
// freely across requests, users and pods. All methods are best-effort: an
// implementation must swallow backend failures and report misses instead —
// the writer degrades to CRC32=0, which extractors treat as "unknown",
// never as a mismatch.
type CRCResumer interface {
	// PutCRC records the checksum of the complete file content.
	PutCRC(ctx context.Context, key string, crc uint32)
	// GetCheckpoint returns the best known running CRC32 state at an
	// offset <= limit. ok is false when nothing usable is stored.
	GetCheckpoint(ctx context.Context, key string, limit int64) (offset int64, state uint32, ok bool)
	// PutCheckpoint records the running CRC32 state after the first
	// offset bytes of the file. Implementations only ever receive
	// states anchored at byte 0 (the writer guarantees this).
	PutCheckpoint(ctx context.Context, key string, offset int64, state uint32)
}

const (
	// DefaultCRCCheckpointInterval is how often a streaming pass persists
	// its running CRC state. It bounds the upstream re-read a later resume
	// needs to re-anchor: the nearest checkpoint is at most one interval
	// below the resume offset.
	DefaultCRCCheckpointInterval = int64(16 << 20)
	// MaxCRCGapFetch bounds the server-side upstream read used to bridge
	// the gap between the best checkpoint and the resume offset. Beyond
	// it we give up on the CRC (emit 0) rather than hammer the seeder.
	MaxCRCGapFetch = int64(64 << 20)
)

// crcTracker follows the streamed bytes of one file, maintaining the
// running CRC32 state anchored at byte 0 and persisting periodic
// checkpoints so an interrupted download can be resumed with a correct
// checksum later.
type crcTracker struct {
	ctx      context.Context
	crcs     CRCResumer
	key      string
	state    uint32
	offset   int64
	interval int64
	nextCkpt int64
}

func newCRCTracker(ctx context.Context, crcs CRCResumer, key string, state uint32, offset int64, interval int64) *crcTracker {
	return &crcTracker{ctx: ctx, crcs: crcs, key: key, state: state, offset: offset, interval: interval, nextCkpt: offset + interval}
}

func (t *crcTracker) Write(p []byte) (int, error) {
	t.state = crc32.Update(t.state, crc32.IEEETable, p)
	t.offset += int64(len(p))
	if t.crcs != nil && t.key != "" && t.offset >= t.nextCkpt {
		t.crcs.PutCheckpoint(t.ctx, t.key, t.offset, t.state)
		t.nextCkpt = t.offset + t.interval
	}
	return len(p), nil
}

// discard consumes r fully, feeding it into the tracker only.
func (t *crcTracker) discard(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{t}, r)
}
