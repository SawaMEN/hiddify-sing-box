package v2rayhttpupgrade

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type xuiUpgradeWriter struct {
	*httptest.ResponseRecorder
	conn   net.Conn
	reader *bufio.ReadWriter
}

func (w *xuiUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, w.reader, nil
}

type xuiUpgradeHandler struct {
	t      *testing.T
	want   string
	called bool
}

func (h *xuiUpgradeHandler) NewConnectionEx(_ context.Context, c net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	h.called = true
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	got := make([]byte, len(h.want))
	_, err := io.ReadFull(c, got)
	if err != nil || string(got) != h.want {
		h.t.Fatalf("lost buffered upgrade payload: %q %v", got, err)
	}
}

func TestXUIPanelHTTPUpgradePreservesHijackBuffer(t *testing.T) {
	const payload = "0123456789abcdef-proxy-authentication-header"
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	reader := bufio.NewReader(bytes.NewBufferString(payload))
	if _, err := reader.Peek(len(payload)); err != nil {
		t.Fatal(err)
	}
	writer := &xuiUpgradeWriter{httptest.NewRecorder(), c, bufio.NewReadWriter(reader, bufio.NewWriter(io.Discard))}
	handler := &xuiUpgradeHandler{t: t, want: payload}
	server := &Server{path: "/audit", handler: handler}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/audit", nil)
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	server.ServeHTTP(writer, req)
	if !handler.called {
		t.Fatal("upgrade handler not called")
	}
}

type xuiUpgradeDialer struct{ conn net.Conn }

func (d xuiUpgradeDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

func (d xuiUpgradeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	panic("unused")
}

func TestXUIPanelHTTPUpgradePreservesResponseBuffer(t *testing.T) {
	const payload = "first-proxy-response"
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	sent := make(chan error, 1)
	go func() {
		_, err := http.ReadRequest(bufio.NewReader(peer))
		if err == nil {
			_, err = io.WriteString(peer, "HTTP/1.1 101 Switching Protocols\r\nConnection: upgrade\r\nUpgrade: websocket\r\n\r\n"+payload)
		}
		sent <- err
	}()
	client, err := NewClient(context.Background(), xuiUpgradeDialer{c}, M.ParseSocksaddr("127.0.0.1:80"), option.V2RayHTTPUpgradeOptions{Path: "/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	_, err = io.ReadFull(conn, got)
	if err != nil || string(got) != payload {
		t.Fatalf("lost buffered response: %q %v", got, err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}
