// Package iptunnel joins a packet transport to an isolated userspace IP stack.
// It does not create an OS TUN or require root privileges.
package iptunnel

import (
	"context"
	"fmt"
	legacy "github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/sagernet/sing-box/transport/iptunnel/netstack"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	"net/netip"
	"sync"
)

type Network struct {
	addresses []netip.Addr
	Resolve   func(context.Context, M.Socksaddr) (M.Socksaddr, error)
	device    legacy.Device
	stack     *netstack.Net
	once      sync.Once
	done      chan struct{}
	send      func([]byte) error
	err       error
}

func New(addresses []netip.Addr, mtu int, send func([]byte) error) (*Network, error) {
	d, s, err := netstack.CreateNetTUN(addresses, nil, mtu)
	if err != nil {
		return nil, err
	}
	n := &Network{device: d, stack: s, send: send, done: make(chan struct{})}
	n.addresses = append([]netip.Addr(nil), addresses...)
	go n.pump(mtu)
	return n, nil
}
func (n *Network) pump(mtu int) {
	b := make([]byte, mtu+256)
	for {
		sizes := []int{0}
		_, err := n.device.Read([][]byte{b}, sizes, 0)
		if err != nil {
			return
		}
		if err = n.send(b[:sizes[0]]); err != nil {
			n.Close()
			return
		}
	}
}
func (n *Network) Inject(p []byte) error { _, err := n.device.Write([][]byte{p}, 0); return err }
func (n *Network) DialContext(ctx context.Context, network string, dst M.Socksaddr) (net.Conn, error) {
	return n.stack.DialContext(ctx, network, dst.String())
}
func (n *Network) ListenPacket(ctx context.Context, dst M.Socksaddr) (net.PacketConn, error) {
	var local netip.Addr
	for _, a := range n.addresses {
		if a.Is6() == dst.Addr.Is6() {
			local = a
			break
		}
	}
	if !local.IsValid() {
		return nil, fmt.Errorf("IP tunnel address family unavailable")
	}
	p, e := n.stack.DialUDPAddrPort(netip.AddrPortFrom(local, 0), netip.AddrPort{})
	if e != nil {
		return nil, e
	}
	return &packetConn{PacketConn: p, ctx: ctx, resolve: n.Resolve}, nil
}
func (n *Network) Close() error {
	n.once.Do(func() { close(n.done); n.err = n.device.Close() })
	return n.err
}

type packetConn struct {
	net.PacketConn
	ctx     context.Context
	resolve func(context.Context, M.Socksaddr) (M.Socksaddr, error)
}

func (p *packetConn) WriteTo(b []byte, a net.Addr) (int, error) {
	d := M.SocksaddrFromNet(a)
	if p.resolve != nil {
		var e error
		d, e = p.resolve(p.ctx, d)
		if e != nil {
			return 0, e
		}
	}
	if !d.Addr.IsValid() {
		return 0, fmt.Errorf("IP tunnel requires a resolved address")
	}
	return p.PacketConn.WriteTo(b, d.UDPAddr())
}
