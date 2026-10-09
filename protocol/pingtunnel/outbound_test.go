//go:build linux

package pingtunnel

import (
	"context"
	"github.com/sagernet/sing-box/option"
	p "github.com/sagernet/sing-box/transport/compat/pingtunnel"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"net"
	"testing"
	"time"
)

func TestICMPTCPUDP(t *testing.T) {
	packetPorts := map[string]int{}
	server, e := p.NewServer("127.0.0.2", 12345, 100, 10, 1000, 10000, nil, nil, "bb")
	if e != nil {
		t.Fatal(e)
	}
	if e = server.Run(); e != nil {
		server.PacketListen = func(string) (*p.PacketConn, error) { return fakeSocket("127.0.0.2", packetPorts) }
		if e = server.Run(); e != nil {
			t.Fatal(e)
		}
	}
	defer server.Stop()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	go func() {
		b := make([]byte, 1500)
		for {
			n, a, e := udp.ReadFrom(b)
			if e != nil {
				return
			}
			udp.WriteTo(b[:n], a)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, e := NewOutbound(ctx, nil, nil, "test", option.PingTunnelOutboundOptions{Server: "127.0.0.2", Key: 12345})
	if e != nil {
		t.Fatal(e)
	}
	h := a.(*Outbound)
	if server.PacketListen != nil {
		h.packetListen = func(string) (*p.PacketConn, error) { return fakeSocket("127.0.0.1", packetPorts) }
	}
	defer h.Close()
	for _, n := range []string{"tcp", "udp"} {
		dst := ln.Addr()
		if n == "udp" {
			dst = udp.LocalAddr()
		}
		c, e := h.DialContext(ctx, n, M.ParseSocksaddr(dst.String()))
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(15 * time.Second))
		payload := []byte("ping tunnel live ICMP")
		if _, e = c.Write(payload); e != nil {
			t.Fatal(e)
		}
		got := make([]byte, len(payload))
		if _, e = io.ReadFull(c, got); e != nil {
			t.Fatal(e)
		}
		c.Close()
		if string(got) != string(payload) {
			t.Fatal("payload mismatch")
		}
	}
}

type fakeICMPSocket struct {
	net.PacketConn
	ports map[string]int
}

func fakeSocket(ip string, ports map[string]int) (*p.PacketConn, error) {
	c, e := net.ListenPacket("udp", ip+":0")
	if e != nil {
		return nil, e
	}
	ports[ip] = c.LocalAddr().(*net.UDPAddr).Port
	return &p.PacketConn{PacketConn: &fakeICMPSocket{c, ports}}, nil
}
func (c *fakeICMPSocket) WriteTo(b []byte, a net.Addr) (int, error) {
	ip := a.(*net.IPAddr).IP.String()
	return c.PacketConn.WriteTo(b, &net.UDPAddr{IP: net.ParseIP(ip), Port: c.ports[ip]})
}
func (c *fakeICMPSocket) ReadFrom(b []byte) (int, net.Addr, error) {
	n, a, e := c.PacketConn.ReadFrom(b)
	if e != nil {
		return n, nil, e
	}
	return n, &net.IPAddr{IP: a.(*net.UDPAddr).IP}, nil
}
