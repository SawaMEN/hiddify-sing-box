package device

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func TestH_UDPReconfigurationClosesChangedFamily(t *testing.T) {
	addresses := []netip.Prefix{netip.MustParsePrefix("172.16.0.2/32"), netip.MustParsePrefix("fd00::2/128")}
	d, err := newStackDevice(Options{Context: context.Background(), Logger: logger.NOP(), MTU: 1280, Configuration: Configuration{Address: addresses}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	require.NoError(t, d.Start())

	connected, err := d.DialContext(context.Background(), "udp", M.ParseSocksaddr("192.0.2.1:53"))
	require.NoError(t, err)
	defer connected.Close()
	packet4, err := d.ListenPacket(context.Background(), M.ParseSocksaddr("192.0.2.1:53"))
	require.NoError(t, err)
	defer packet4.Close()
	packet6, err := d.ListenPacket(context.Background(), M.ParseSocksaddr("[fd00::1]:53"))
	require.NoError(t, err)
	defer packet6.Close()

	// Reapplying the same addresses preserves sockets in both address families.
	require.NoError(t, d.UpdateConfiguration(Configuration{Address: addresses}))
	hPacketTimesOut(t, packet4)
	hPacketTimesOut(t, packet6)

	changed := []netip.Prefix{netip.MustParsePrefix("172.16.0.3/32"), addresses[1]}
	require.NoError(t, d.UpdateConfiguration(Configuration{Address: changed}))
	require.NoError(t, connected.SetReadDeadline(time.Now().Add(time.Second)))
	_, err = connected.Read(make([]byte, 1))
	hRequireClosed(t, err)
	require.NoError(t, packet4.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = packet4.ReadFrom(make([]byte, 1))
	hRequireClosed(t, err)
	hPacketTimesOut(t, packet6)

	// Removing IPv6 invalidates only that family's remaining socket.
	require.NoError(t, d.UpdateConfiguration(Configuration{Address: changed[:1]}))
	require.NoError(t, packet6.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = packet6.ReadFrom(make([]byte, 1))
	hRequireClosed(t, err)
}

func hPacketTimesOut(t *testing.T, conn net.PacketConn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Millisecond)))
	_, _, err := conn.ReadFrom(make([]byte, 1))
	var timeout net.Error
	require.True(t, errors.As(err, &timeout) && timeout.Timeout(), "unchanged socket must remain open: %v", err)
}

func hRequireClosed(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var timeout net.Error
	require.False(t, errors.As(err, &timeout) && timeout.Timeout(), "socket must close immediately after its address changes")
}
