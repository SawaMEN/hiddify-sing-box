package grpcname

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Expected values follow Xray's transport/internet/grpc/config.go (same in 26.3.27 and 26.10.21).
func TestH_Names(t *testing.T) {
	for _, test := range []struct {
		name    string
		service string
		stream  string
		path    string
	}{
		{"", "", "Tun", "//Tun"},
		{"TunService", "TunService", "Tun", "/TunService/Tun"},
		{"a b", "a%20b", "Tun", "/a%20b/Tun"},
		{"/my/path/Tun", "my/path", "Tun", "/my/path/Tun"},
		{"/my/path/Tun|TunMulti", "my/path", "Tun", "/my/path/Tun"},
		{"/my/sample/path1|path2", "my/sample", "path1", "/my/sample/path1"},
		{"/a b/c d", "a%20b", "c%20d", "/a%20b/c%20d"},
		{"/abc", "", "abc", "//abc"},
		{"/", "", "", "//"},
	} {
		service, stream := Names(test.name)
		require.Equal(t, test.service, service, test.name)
		require.Equal(t, test.stream, stream, test.name)
		require.Equal(t, test.path, Path(test.name), test.name)
	}
}

func TestH_MultiPaths(t *testing.T) {
	for _, tc := range []struct {
		name, path, decoded string
	}{
		{"", "//TunMulti", "//TunMulti"},
		{"TunService", "/TunService/TunMulti", "/TunService/TunMulti"},
		{"a b", "/a%20b/TunMulti", "/a b/TunMulti"},
		{"/my/path/Tun|TunMulti", "/my/path/TunMulti", "/my/path/TunMulti"},
		{"/my/path/method", "/my/path/method", "/my/path/method"},
		{"/a b/c d|e f", "/a%20b/e%20f", "/a b/e f"},
	} {
		require.Equal(t, tc.path, MultiPath(tc.name), tc.name)
		require.Equal(t, tc.decoded, UnescapedMultiPath(tc.name), tc.name)
	}
}
