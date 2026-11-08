package option

import (
	"testing"
)

func TestXUIPanelNestedDownloadResolve(t *testing.T) {
	inner := &V2RayXHTTPDownloadOptions{ServerOptions: ServerOptions{Server: "127.0.0.1", ServerPort: 443}}
	outer := &V2RayXHTTPDownloadOptions{ServerOptions: ServerOptions{Server: "127.0.0.1", ServerPort: 444}, Download: inner}
	cfg := &V2RayXHTTPOptions{Mode: "packet-up", Download: outer}
	got, err := cfg.ResolveDownload()
	if err != nil || got != inner {
		t.Fatalf("deepest download endpoint not selected: %p, %v", got, err)
	}
	inner.Download = outer
	if _, err := cfg.ResolveDownload(); err == nil {
		t.Fatal("cyclic download options accepted")
	}
	inner.Download = nil
	cfg.Mode = "stream-one"
	if _, err := cfg.ResolveDownload(); err == nil {
		t.Fatal("stream-one mode with separate download accepted")
	}
	cfg.Mode = "packet-up"
	inner.Mode = "invalid"
	if _, err := cfg.ResolveDownload(); err == nil {
		t.Fatal("invalid download mode accepted")
	}
	inner.Mode = ""
	inner.ServerPort = 0
	if _, err := cfg.ResolveDownload(); err == nil {
		t.Fatal("missing download server port accepted")
	}
}

func TestXUIPanelNestedDownloadDepthLimit(t *testing.T) {
	cfg := &V2RayXHTTPOptions{Mode: "packet-up"}
	root := &V2RayXHTTPDownloadOptions{ServerOptions: ServerOptions{Server: "127.0.0.1", ServerPort: 443}}
	cfg.Download = root
	for range 8 {
		root.Download = &V2RayXHTTPDownloadOptions{ServerOptions: ServerOptions{Server: "127.0.0.1", ServerPort: 443}}
		root = root.Download
	}
	if _, err := cfg.ResolveDownload(); err == nil {
		t.Fatal("download chains deeper than eight accepted")
	}
}
