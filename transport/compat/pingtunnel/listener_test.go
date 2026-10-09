package pingtunnel

import (
	"net"
	"testing"
)

func TestRunListenerFailureClosesPacketSocket(t *testing.T) {
	occupied, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	c, err := NewClient(occupied.Addr().String(), "127.0.0.1", "", 60, 1, "0.0.0.0", 1, 1<<20, 100, 400, 0, 0, 1, 1024, nil, "", "", "bb")
	if err != nil {
		t.Fatal(err)
	}
	c.PacketListen = func(string) (*PacketConn, error) { return &PacketConn{packet}, nil }
	if c.Run() == nil {
		t.Fatal("expected occupied listener error")
	}
	if _, err := packet.WriteTo([]byte("leak"), packet.LocalAddr()); err == nil {
		t.Fatal("startup failure leaked ICMP socket")
	}
}
