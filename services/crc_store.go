package services

import (
	"context"
	"crypto/sha1"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"github.com/webtor-io/torrent-archiver/ziphttp"
)

var _ ziphttp.CRCResumer = (*HashCRCResumer)(nil)

// CRCStore keeps content-derived CRC32 knowledge in Redis so interrupted
// zip downloads can be resumed with correct checksums (see ziphttp.CRCResumer).
// Values are pure functions of torrent content, shared across pods and users:
//   - arch:crc:<ih>:<sha1(path)>  → full-file CRC32 (30d TTL, refreshed on write)
//   - arch:ck:<ih>:<sha1(path)>   → hash of running states: field=offset,
//     value=state (48h TTL — only useful while someone is mid-download)
type CRCStore struct {
	cl redis.UniversalClient
}

const (
	crcTTL       = 30 * 24 * time.Hour
	crcCkptTTL   = 48 * time.Hour
	crcOpTimeout = 500 * time.Millisecond

	enableCRCCacheFlag = "enable-crc-cache"
)

func RegisterCRCStoreFlags(f []cli.Flag) []cli.Flag {
	return append(f,
		cli.BoolFlag{
			Name:   enableCRCCacheFlag,
			Usage:  "persist zip CRC32 state in redis so resumed downloads keep correct checksums",
			EnvVar: "ENABLE_CRC_CACHE",
		},
	)
}

func CRCCacheEnabled(c *cli.Context) bool {
	return c.Bool(enableCRCCacheFlag)
}

func NewCRCStore(cl redis.UniversalClient) *CRCStore {
	return &CRCStore{cl: cl}
}

// Resumer returns a per-request ziphttp.CRCResumer scoped to one torrent.
// Every operation is best-effort with a short timeout; the first backend
// failure disables the rest of the request's operations so a dead Redis
// costs one timeout, not one per file.
func (s *CRCStore) Resumer(infoHash string) *HashCRCResumer {
	if s == nil {
		return nil
	}
	return &HashCRCResumer{cl: s.cl, ih: infoHash}
}

type HashCRCResumer struct {
	cl     redis.UniversalClient
	ih     string
	failed bool
}

func (s *HashCRCResumer) key(path string) string {
	return fmt.Sprintf("arch:crc:%s:%x", s.ih, sha1.Sum([]byte(path)))
}

func (s *HashCRCResumer) ckKey(path string) string {
	return fmt.Sprintf("arch:ck:%s:%x", s.ih, sha1.Sum([]byte(path)))
}

func (s *HashCRCResumer) fail(op string, err error) {
	s.failed = true
	log.WithError(err).Warnf("crc store degraded, skipping further ops for this request (op=%s)", op)
}

func (s *HashCRCResumer) PutCRC(ctx context.Context, path string, crc uint32) {
	if s.failed {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, crcOpTimeout)
	defer cancel()
	if err := s.cl.Set(ctx, s.key(path), strconv.FormatUint(uint64(crc), 10), crcTTL).Err(); err != nil {
		s.fail("put-crc", err)
	}
}

func (s *HashCRCResumer) GetCheckpoint(ctx context.Context, path string, limit int64) (offset int64, state uint32, ok bool) {
	if s.failed {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, crcOpTimeout)
	defer cancel()
	m, err := s.cl.HGetAll(ctx, s.ckKey(path)).Result()
	if err != nil {
		s.fail("get-checkpoint", err)
		return 0, 0, false
	}
	for o, v := range m {
		po, err1 := strconv.ParseInt(o, 10, 64)
		pv, err2 := strconv.ParseUint(v, 10, 32)
		if err1 != nil || err2 != nil || po > limit {
			continue
		}
		if !ok || po > offset {
			offset, state, ok = po, uint32(pv), true
		}
	}
	return offset, state, ok
}

func (s *HashCRCResumer) PutCheckpoint(ctx context.Context, path string, offset int64, state uint32) {
	if s.failed {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, crcOpTimeout)
	defer cancel()
	k := s.ckKey(path)
	pipe := s.cl.Pipeline()
	pipe.HSet(ctx, k, strconv.FormatInt(offset, 10), strconv.FormatUint(uint64(state), 10))
	pipe.Expire(ctx, k, crcCkptTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		s.fail("put-checkpoint", err)
	}
}

// Preseed fetches the known full-file CRCs for the given torrent paths in
// one MGET. Missing or unparsable values are simply absent from the map.
func (s *HashCRCResumer) Preseed(ctx context.Context, paths []string) map[string]uint32 {
	res := map[string]uint32{}
	if s.failed || len(paths) == 0 {
		return res
	}
	keys := make([]string, len(paths))
	for i, p := range paths {
		keys[i] = s.key(p)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	vals, err := s.cl.MGet(ctx, keys...).Result()
	if err != nil {
		s.fail("preseed", err)
		return res
	}
	for i, v := range vals {
		sv, sok := v.(string)
		if !sok {
			continue
		}
		pv, perr := strconv.ParseUint(sv, 10, 32)
		if perr != nil {
			continue
		}
		res[paths[i]] = uint32(pv)
	}
	return res
}
