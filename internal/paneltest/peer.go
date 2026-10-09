// Package paneltest supplies an IP peer for protocol interoperability tests.
package paneltest

import (
	"github.com/sagernet/sing-box/transport/iptunnel/netstack"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type Peer struct {
	Inject func([]byte)
	Close  func()
}

func NewPeer(t *testing.T, addresses []netip.Addr, send func([]byte) error) Peer {
	t.Helper()
	device, stack, e := netstack.CreateNetTUN(addresses, nil, 1500)
	if e != nil {
		t.Fatal(e)
	}
	go func() {
		b := make([]byte, 2000)
		sizes := []int{0}
		for {
			_, e := device.Read([][]byte{b}, sizes, 0)
			if e != nil {
				return
			}
			if send(b[:sizes[0]]) != nil {
				return
			}
		}
	}()

	var listeners []net.Listener
	var packets []net.PacketConn
	for _, address := range addresses {
		ln, e := stack.ListenTCPAddrPort(netip.AddrPortFrom(address, 8080))
		if e != nil {
			t.Fatal(e)
		}
		listeners = append(listeners, ln)
		go func() {
			for {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				go func() { defer c.Close(); io.Copy(c, c) }()
			}
		}()
		udp, e := stack.DialUDPAddrPort(netip.AddrPortFrom(address, 8081), netip.AddrPort{})
		if e != nil {
			t.Fatal(e)
		}
		packets = append(packets, udp)
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
	}
	var once sync.Once
	close := func() {
		once.Do(func() {
			for _, l := range listeners {
				l.Close()
			}
			for _, p := range packets {
				p.Close()
			}
			device.Close()
		})
	}
	t.Cleanup(close)
	return Peer{Inject: func(p []byte) { device.Write([][]byte{p}, 0) }, Close: close}
}
func Echo(t *testing.T, c net.Conn) {
	t.Helper()
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	payload := []byte("native transport TCP/UDP interoperability")
	if _, e := c.Write(payload); e != nil {
		t.Fatal(e)
	}
	got := make([]byte, len(payload))
	if _, e := io.ReadFull(c, got); e != nil {
		t.Fatal(e)
	}
	if string(got) != string(payload) {
		t.Fatal("payload mismatch")
	}
}
