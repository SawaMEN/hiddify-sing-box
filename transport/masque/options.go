package masque

import (
	"net"
	"net/netip"
	"time"

	tun "github.com/sagernet/sing-tun"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/tls"
)

type TunnelOptions struct {
	Handler              tun.Handler
	Dialer               N.Dialer
	Address              []netip.Prefix
	Endpoint             net.Addr
	TLSConfig            tls.Config
	UseHTTP2             bool
	Mode                 string                                                     // H
	H2Endpoint           net.Addr                                                   // H
	ConnectURI           string                                                     // H
	OnConnected          func(endpoint net.Addr, http2 bool, elapsed time.Duration) // H
	NextEndpoint         func(failed net.Addr, http2 bool) net.Addr                 // H
	UDPTimeout           time.Duration
	UDPKeepalivePeriod   time.Duration
	UDPInitialPacketSize uint16
	ReconnectDelay       time.Duration
}
