package trojan

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type xuiFragmentedConn struct{ net.Conn }

func (c xuiFragmentedConn) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return c.Conn.Read(p)
}

type xuiTrojanHandler struct {
	payload []byte
	err     error
}

func (h *xuiTrojanHandler) NewConnectionEx(_ context.Context, c net.Conn, _, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	h.payload = make([]byte, 5)
	_, h.err = io.ReadFull(c, h.payload)
}
func (h *xuiTrojanHandler) NewPacketConnectionEx(context.Context, N.PacketConn, M.Socksaddr, M.Socksaddr, N.CloseHandlerFunc) {
	panic("unused")
}

func TestXUIPanelTrojanFragmentedAuthentication(t *testing.T) {
	handler := &xuiTrojanHandler{}
	service := NewService[int](handler, nil, nil)
	if err := service.UpdateUsers([]int{1}, []string{"secret"}); err != nil {
		t.Fatal(err)
	}
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_ = peer.SetDeadline(time.Now().Add(time.Second))
	var header bytes.Buffer
	key := Key("secret")
	header.Write(key[:])
	header.WriteString("\r\n")
	header.WriteByte(CommandTCP)
	if err := M.SocksaddrSerializer.WriteAddrPort(&header, M.ParseSocksaddr("[::1]:443")); err != nil {
		t.Fatal(err)
	}
	header.WriteString("\r\nhello")
	sent := make(chan error, 1)
	go func() { _, err := peer.Write(header.Bytes()); sent <- err }()
	if err := service.NewConnection(context.Background(), xuiFragmentedConn{c}, M.Socksaddr{}, nil); err != nil {
		t.Fatal(err)
	}
	if handler.err != nil || string(handler.payload) != "hello" {
		t.Fatalf("fragmented authentication failed: %q %v", handler.payload, handler.err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

// A short non-Trojan HTTP request must fall back without waiting for 56 bytes.
func TestXUIPanelTrojanShortHTTPFallback(t *testing.T) {
	handler := &xuiTrojanHandler{}
	service := NewService[int](handler, handler, nil)
	c, peer := net.Pipe()
	defer c.Close()
	defer peer.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_ = peer.SetDeadline(time.Now().Add(time.Second))
	go func() { _, _ = peer.Write([]byte("GET /")) }()
	if err := service.NewConnection(context.Background(), c, M.Socksaddr{}, nil); err != nil {
		t.Fatal(err)
	}
	if handler.err != nil || string(handler.payload) != "GET /" {
		t.Fatalf("fallback lost prefix: %q %v", handler.payload, handler.err)
	}
}
