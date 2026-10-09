package openflux

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/internal/paneltest"
	"github.com/sagernet/sing-box/option"
	tr "github.com/sagernet/sing-box/transport/compat/openflux/transport"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestNegotiatedDirectTCPUDP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	l.Close()
	secret := "0123456789abcdef0123456789abcdef"
	server, e := tr.NewSession(tr.PeerParameters{Capabilities: tr.CapabilityIPv4 | tr.CapabilityTCP | tr.CapabilityUDP, MaxPacketSize: tr.MaxNegotiatedPacket}, true)
	if e != nil {
		t.Fatal(e)
	}
	config := tr.DefaultDirectConfig()
	config.IsExit = true
	config.ListenAddr = addr
	raw := tr.NewDirectTransport(tr.DefaultConfig(), config)
	if e = server.AddTransport("direct", raw, secret, "interop", 0); e != nil {
		t.Fatal(e)
	}
	peer := paneltest.NewPeer(t, []netip.Addr{netip.MustParseAddr("10.10.10.1")}, server.Send)
	server.Receive(peer.Inject)
	if e = server.Start(); e != nil {
		t.Fatal(e)
	}
	defer server.Stop()
	a, e := newOutboundWithDialer(ctx, "test", option.OpenFluxOutboundOptions{Secret: secret, Context: "interop", Codec: "batched", Negotiate: true, Transports: []option.OpenFluxTransportOptions{{Type: "direct", Dial: addr}}}, testDialer{})
	if e != nil {
		t.Fatal(e)
	}
	h := a.(*Outbound)
	defer h.Close()
	if e = h.Start(adapter.StartStateStart); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"tcp", "udp"} {
		port := uint16(8080)
		if n == "udp" {
			port = 8081
		}
		c, e := h.DialContext(ctx, n, M.SocksaddrFrom(netip.MustParseAddr("10.10.10.1"), port))
		if e != nil {
			t.Fatal(e)
		}
		paneltest.Echo(t, c)
	}
	packet, e := h.ListenPacket(ctx, M.SocksaddrFrom(netip.MustParseAddr("10.10.10.1"), 8081))
	if e != nil {
		t.Fatal(e)
	}
	packet.SetDeadline(time.Now().Add(10 * time.Second))
	data := []byte("unconnected UDP")
	if _, e = packet.WriteTo(data, M.SocksaddrFrom(netip.MustParseAddr("10.10.10.1"), 8081)); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1500)
	n, _, e := packet.ReadFrom(b)
	if e != nil || string(b[:n]) != string(data) {
		t.Fatalf("UDP packet round trip: %v", e)
	}
	packet.Close()

}

type testDialer struct{}

func (testDialer) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, n, d.String())
}
func (testDialer) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	return net.ListenPacket("udp", "127.0.0.1:0")
}
