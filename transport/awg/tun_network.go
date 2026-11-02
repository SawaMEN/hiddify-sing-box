package awg

import (
	"context"
	"net"
	"net/netip"
	"sync"

	"github.com/sagernet/sing/common/metadata"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
)

type networkTun struct {
	tun.Device
	conn      *netstack.Net
	events    chan tun.Event
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newNetworkTun(address []netip.Prefix, mtu uint32) (tunAdapter, error) {
	var localAddresses []netip.Addr
	for _, prefix := range address {
		localAddresses = append(localAddresses, prefix.Addr())
	}

	device, conn, err := netstack.CreateNetTUN(localAddresses, []netip.Addr{}, int(mtu))
	if err != nil {
		return nil, err
	}

	// Use the same v3 netstack and device types as the AWG engine and MTU discovery.
	result := &networkTun{Device: device, conn: conn, events: make(chan tun.Event, 1), done: make(chan struct{})}
	go func() {
		defer close(result.events)
		for {
			select {
			case <-result.done:
				return
			case event, ok := <-device.Events():
				if !ok {
					return
				}
				select {
				case result.events <- tun.Event(event):
				case <-result.done:
					return
				}
			}
		}
	}()
	return result, nil
}

func (t *networkTun) Events() <-chan tun.Event { return t.events }
func (t *networkTun) Close() error {
	t.closeOnce.Do(func() { close(t.done); t.closeErr = t.Device.Close() })
	return t.closeErr
}

func (t *networkTun) Start() error {
	return nil
}

func (t *networkTun) DialContext(ctx context.Context, network string, destination metadata.Socksaddr) (net.Conn, error) {
	return t.conn.DialContext(ctx, network, destination.String())
}

func (t *networkTun) ListenPacket(ctx context.Context, destination metadata.Socksaddr) (net.PacketConn, error) {
	return t.conn.DialUDPAddrPort(netip.AddrPort{}, destination.AddrPort())
}
