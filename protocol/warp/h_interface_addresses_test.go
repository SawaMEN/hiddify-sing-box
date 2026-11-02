package warp

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestH_WARPInterfaceAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, ipv4, ipv6 string
		want             []netip.Prefix
	}{
		{"ipv4", "172.16.9.2", "", []netip.Prefix{netip.MustParsePrefix("172.16.9.2/32")}},
		{"ipv6", "", "2606:4700::2", []netip.Prefix{netip.MustParsePrefix("2606:4700::2/128")}},
		{"dual", "172.16.9.2", "2606:4700::2", []netip.Prefix{netip.MustParsePrefix("172.16.9.2/32"), netip.MustParsePrefix("2606:4700::2/128")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addresses, err := warpInterfaceAddresses(tc.ipv4, tc.ipv6)
			require.NoError(t, err)
			require.Equal(t, tc.want, []netip.Prefix(addresses))
		})
	}
	for _, tc := range [][2]string{{"", ""}, {"not-ip", ""}, {"::1", ""}, {"", "127.0.0.1"}} {
		_, err := warpInterfaceAddresses(tc[0], tc[1])
		require.Error(t, err)
	}
}
