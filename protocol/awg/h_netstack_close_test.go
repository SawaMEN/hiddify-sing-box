//go:build with_awg

package awg

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
	"github.com/stretchr/testify/require"
)

func TestH_NetstackCloseWhileWriting(t *testing.T) {
	for range 20 {
		device, stack, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil, 1420)
		require.NoError(t, err)
		conn, err := stack.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.0.0.2:53"))
		require.NoError(t, err)
		var writers sync.WaitGroup
		for range 4 {
			writers.Go(func() {
				for range 100 {
					if _, err := conn.Write([]byte("ping")); err != nil {
						return
					}
				}
			})
		}
		buffers, sizes := [][]byte{make([]byte, 2048)}, []int{0}
		_, err = device.Read(buffers, sizes, 0)
		require.NoError(t, err)
		require.NoError(t, device.Close())
		require.NoError(t, device.Close())
		writers.Wait()
		_ = conn.Close()
		_, err = device.Read(buffers, sizes, 0)
		require.Error(t, err)
	}
}
