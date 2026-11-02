package transport

import (
	"context"
	"net"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func hSDNSDialPacket(ctx context.Context, dialer N.Dialer, network, address string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, N.NetworkName(network), M.ParseSocksaddr(address))
	if err != nil {
		return nil, err
	}
	return &hSDNSPacketConn{Conn: conn}, nil
}

// DNSCrypt's certificate fetch uses miekg/dns, which detects UDP through
// net.PacketConn. Dialer wrappers expose net.Conn and would otherwise cause
// TCP length framing to be written into a UDP datagram.
type hSDNSPacketConn struct {
	net.Conn
}

func (c *hSDNSPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Read(p)
	return n, c.RemoteAddr(), err
}

func (c *hSDNSPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Write(p)
}

var _ net.PacketConn = (*hSDNSPacketConn)(nil)
