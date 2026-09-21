package services

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestIsPadFile(t *testing.T) {
	cases := []struct {
		name string
		f    metainfo.FileInfo
		want bool
	}{
		{"attr p", metainfo.FileInfo{Path: []string{"x"}, ExtendedFileAttrs: metainfo.ExtendedFileAttrs{Attr: "p"}}, true},
		{"libtorrent name", metainfo.FileInfo{Path: []string{".pad", "1048576"}}, true},
		{"utf8 path", metainfo.FileInfo{Path: []string{"junk"}, PathUtf8: []string{".pad", "7"}}, true},
		{"bitcomet name", metainfo.FileInfo{Path: []string{"_____padding_file_0_if you see this file, please update to BitComet 0.85 or above____"}}, true},
		{"real file", metainfo.FileInfo{Path: []string{"S01", "E01.mkv"}}, false},
		{"pad not leading", metainfo.FileInfo{Path: []string{"S01", ".pad"}}, false},
		{"single-file torrent", metainfo.FileInfo{}, false},
	}
	for _, c := range cases {
		if got := isPadFile(&c.f); got != c.want {
			t.Errorf("%s: isPadFile = %v, want %v", c.name, got, c.want)
		}
	}
}
