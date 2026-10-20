package services

import (
	"strings"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestGetPathDropsEmptyComponents(t *testing.T) {
	info := &metainfo.Info{Name: "Name"}
	cases := []struct {
		name string
		f    metainfo.FileInfo
		want string
	}{
		{"empty in the middle", metainfo.FileInfo{Path: []string{"", "4-03.mp3"}}, "Name/4-03.mp3"},
		{"utf8 path wins", metainfo.FileInfo{Path: []string{"x"}, PathUtf8: []string{"S01", "", "E01.mkv"}}, "Name/S01/E01.mkv"},
		{"plain", metainfo.FileInfo{Path: []string{"S01", "E01.mkv"}}, "Name/S01/E01.mkv"},
		{"single-file torrent", metainfo.FileInfo{}, "Name"},
	}
	for _, c := range cases {
		if got := strings.Join(getPath(info, &c.f), "/"); got != c.want {
			t.Errorf("%s: getPath = %q, want %q", c.name, got, c.want)
		}
	}
}

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
