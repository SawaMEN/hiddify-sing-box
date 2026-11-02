package warp

import (
	"net/netip"

	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
)

// warpInterfaceAddresses accepts IPv4-only and IPv6-only WARP profiles,
// rather than panicking when an optional address is missing.
func warpInterfaceAddresses(ipv4, ipv6 string) (badoption.Listable[netip.Prefix], error) {
	var result badoption.Listable[netip.Prefix]
	if ipv4 != "" {
		addr, err := netip.ParseAddr(ipv4)
		if err != nil || !addr.Is4() {
			return nil, E.New("invalid WARP interface IPv4 address: ", ipv4)
		}
		result = append(result, netip.PrefixFrom(addr, 32))
	}
	if ipv6 != "" {
		addr, err := netip.ParseAddr(ipv6)
		if err != nil || !addr.Is6() || addr.Is4In6() {
			return nil, E.New("invalid WARP interface IPv6 address: ", ipv6)
		}
		result = append(result, netip.PrefixFrom(addr, 128))
	}
	if len(result) == 0 {
		return nil, E.New("WARP interface is missing IPv4 and IPv6 addresses")
	}
	return result, nil
}
