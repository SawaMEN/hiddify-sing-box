package xhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestH_XHTTPServerAcceptsObfuscatedPadding(t *testing.T) {
	server := hNewServer(t, `{"mode":"packet-up",`+hBase+`,"x_padding_obfs_mode":true,"x_padding_placement":"header","x_padding_header":"X-Pad","x_padding_key":"x_pad"}`)
	request := hRequest("POST", "/xh/s/0", []byte("payload"), -1)
	request.Header.Set("X-Pad", strings.Repeat("X", 120))
	require.Equal(t, http.StatusOK, hServe(server, request).Code)
}

func TestH_XHTTPClientAutoMode(t *testing.T) {
	for _, testCase := range []struct {
		mode     string
		reality  bool
		download bool
		want     string
	}{
		{"auto", false, false, "packet-up"},
		{"auto", false, true, "packet-up"},
		{"auto", true, false, "stream-one"},
		{"auto", true, true, "stream-up"},
		{"packet-up", true, false, "packet-up"},
		{"stream-up", true, false, "stream-up"},
		{"stream-one", false, true, "stream-one"},
	} {
		require.Equal(t, testCase.want, hClientMode(testCase.mode, testCase.reality, testCase.download))
	}
}
