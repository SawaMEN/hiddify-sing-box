package xhttp

import (
	"fmt"
	"testing"
)

func TestXUIPanelNestedDownloadRoundTrip(t *testing.T) {
	addr := hStartServer(t, hOptions(t, `{"mode":"auto","path":"/nested-download"}`), nil)
	for _, mode := range []string{"packet-up", "stream-up"} {
		t.Run(mode, func(t *testing.T) {
			cfg := fmt.Sprintf(`{"mode":%q,"path":"/nested-download","download":{"server":"127.0.0.1","server_port":1,"path":"/unused","download":{"server":"127.0.0.1","server_port":%d,"path":"/nested-download"}}}`, mode, addr.Port)
			hRoundTrip(t, hOptions(t, cfg), nil, addr, hPayloads(t, 128, 4096)...)
		})
	}
}
