package awg

import (
	"net/netip"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

func singlePeer(peers []option.AwgPeerOptions) netip.AddrPort {
	if len(peers) != 1 || peers[0].Port == 0 {
		return netip.AddrPort{}
	}
	address, err := netip.ParseAddr(peers[0].Address)
	if err != nil {
		return netip.AddrPort{}
	}
	return netip.AddrPortFrom(address, peers[0].Port)
}

// peerFallback is the only peer's fallback addresses, on its port (see transport/awg/h_path.go).
// They need a single peer given by an IP address and a port.
func peerFallback(options option.AwgEndpointOptions) ([]netip.AddrPort, error) {
	if len(options.FallbackAddress) == 0 {
		return nil, nil
	}
	peer := singlePeer(options.Peers)
	if !peer.IsValid() {
		return nil, E.New("awg: fallback_address needs a single peer with an IP address and a port")
	}
	addresses := make([]netip.AddrPort, 0, len(options.FallbackAddress))
	for _, address := range options.FallbackAddress {
		addresses = append(addresses, netip.AddrPortFrom(address, peer.Port()))
	}
	return addresses, nil
}
