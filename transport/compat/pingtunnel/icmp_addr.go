package pingtunnel

import (
	"net"
	"sync/atomic"
)

var icmpDatagram atomic.Bool

func setICMPDatagram(enabled bool) {
	icmpDatagram.Store(enabled)
}

func icmpDstAddr(ip *net.IPAddr) net.Addr {
	if icmpDatagram.Load() {
		return &net.UDPAddr{IP: ip.IP}
	}
	return ip
}

func icmpSrcToIPAddr(addr net.Addr) *net.IPAddr {
	switch v := addr.(type) {
	case *net.IPAddr:
		return v
	case *net.UDPAddr:
		return &net.IPAddr{IP: v.IP, Zone: v.Zone}
	default:
		return &net.IPAddr{IP: net.IPv4zero}
	}
}
