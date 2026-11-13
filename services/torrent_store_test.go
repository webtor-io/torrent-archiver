package services

import (
	"bytes"
	"context"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	ts "github.com/webtor-io/torrent-store/proto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// pullFake answers Pull as torrent-store does under PRIVATE_ACCESS_ENFORCE: a
// private torrent only to a caller that says who it is (decision 34).
type pullFake struct {
	ts.TorrentStoreClient
	torrent []byte
	got     []*ts.PullRequest
}

func (f *pullFake) Pull(_ context.Context, in *ts.PullRequest, _ ...grpc.CallOption) (*ts.PullReply, error) {
	f.got = append(f.got, in)
	if in.GetCaller() == nil {
		st, _ := status.New(codes.PermissionDenied, "private torrent").
			WithDetails(&errdetails.ErrorInfo{Domain: "torrent-store", Reason: "sign_in"})
		return nil, st.Err()
	}
	return &ts.PullReply{Torrent: f.torrent}, nil
}

// The archiver's file list is cached by infohash for everyone and thp has decided
// access already, so it pulls as an internal caller and builds a private
// torrent's archive under enforce.
func TestTorrentStore_PullsAsInternal(t *testing.T) {
	yes := true
	info := metainfo.Info{Name: "payload.bin", PieceLength: 16 << 10, Length: 1, Pieces: make([]byte, 20), Private: &yes}
	mi := metainfo.MetaInfo{
		InfoBytes:    bencode.MustMarshal(info),
		AnnounceList: metainfo.AnnounceList{{"http://tracker.invalid/announce?passkey=00000000000000000000000000000000"}},
	}
	var b bytes.Buffer
	if err := mi.Write(&b); err != nil {
		t.Fatal(err)
	}
	f := &pullFake{torrent: b.Bytes()}
	files, err := NewTorrentStore(&TorrentStoreClient{cl: f, inited: true}).Get(mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatalf("Get: %v; a private torrent has to be pulled as internal", err)
	}
	if len(files) != 1 || files[0].path != "payload.bin" {
		t.Fatalf("files = %+v, want payload.bin", files)
	}
	if len(f.got) != 1 {
		t.Fatalf("pulls = %d, want 1", len(f.got))
	}
	if c := f.got[0].GetCaller(); !c.GetInternal() || c.GetUid() != "" {
		t.Fatalf("Pull caller = %v, want {internal: true}", c)
	}
}
