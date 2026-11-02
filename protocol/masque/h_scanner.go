package masque

import (
	"context"
	"net"
	"net/netip"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/masque"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// tunnelMode returns the HTTP version from vhttp, and use_http2 when vhttp is empty.
func tunnelMode(options option.MASQUEOutboundOptions) (string, error) {
	switch options.VHTTP {
	case "":
		if options.UseHTTP2 {
			return masque.ModeHTTP2, nil
		}
		return masque.ModeAuto, nil
	case masque.ModeHTTP3, masque.ModeHTTP2, masque.ModeAuto:
		return options.VHTTP, nil
	}
	return "", E.New("unknown vhttp: ", options.VHTTP, ` (expected "h3", "h2" or "auto")`)
}

func endpointAddr(addrPort netip.AddrPort, http2 bool) net.Addr {
	if http2 {
		return net.TCPAddrFromAddrPort(addrPort)
	}
	return net.UDPAddrFromAddrPort(addrPort)
}

func addrPortOf(addr net.Addr) (netip.AddrPort, bool) {
	switch addr := addr.(type) {
	case *net.UDPAddr:
		return addr.AddrPort(), true
	case *net.TCPAddr:
		return addr.AddrPort(), true
	}
	return netip.AddrPort{}, false
}

// selectEndpoints picks the HTTP/3 and, for auto, the HTTP/2 endpoint: peer when it is set
// (the warp endpoint's server and server_port), the scanner's choice when it finds one,
// otherwise the profile's. The profile has no HTTP/2 IPv6 endpoint, so with use_ipv6 and no
// scanner result auto keeps to HTTP/3 instead of failing.
func selectEndpoints(ctx context.Context, logger log.ContextLogger, config *Config, mode string, useIPv6 bool, peer M.Socksaddr, scan func(http2 bool) (net.Addr, error)) (endpoint net.Addr, h2Endpoint net.Addr, _ string, _ error) {
	choose := func(http2 bool) (net.Addr, error) {
		if hasPeer(peer) {
			return fixedEndpoint(config, peer, http2, useIPv6)
		}
		if scan != nil {
			scanned, err := scan(http2)
			if err == nil {
				return scanned, nil
			}
			logger.WarnContext(ctx, err, ", using the profile's endpoint")
		}
		return config.SelectEndpointFromConfig(http2, useIPv6, profilePort)
	}
	endpoint, err := choose(mode == masque.ModeHTTP2)
	if err != nil {
		return nil, nil, "", err
	}
	if mode == masque.ModeAuto {
		h2Endpoint, err = choose(true)
		if err != nil {
			logger.WarnContext(ctx, "no HTTP/2 endpoint (", err, "), using HTTP/3 only")
			return endpoint, nil, masque.ModeHTTP3, nil
		}
	}
	return endpoint, h2Endpoint, mode, nil
}

// profilePort is the port of the profile's MASQUE endpoints.
const profilePort = 443

// fixedEndpoint is peer, with the profile's address when peer has only a port.
func fixedEndpoint(config *Config, peer M.Socksaddr, http2 bool, useIPv6 bool) (net.Addr, error) {
	port := peer.Port
	if port == 0 {
		port = profilePort
	}
	if !peer.Addr.IsValid() {
		return config.SelectEndpointFromConfig(http2, useIPv6, int(port))
	}
	return endpointAddr(netip.AddrPortFrom(peer.Addr, port), http2), nil
}
