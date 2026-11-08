package inbound

import (
	"slices"

	"github.com/sagernet/sing-box/hiddify/finalmask"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
)

// Validate at construction so `check` fails before any socket is opened.
func validateTCPFinalMask(options any) error {
	wrapper, ok := options.(option.ListenOptionsWrapper)
	if !ok {
		return nil
	}
	mask := wrapper.TakeListenOptions().FinalMask
	if mask == nil {
		return nil
	}
	if len(mask.UDP) > 0 || len(mask.QuicParams) > 0 {
		return E.New("inbound final_mask only supports TCP masks; use protocol-native UDP/QUIC options")
	}
	if mask.IsEmpty() {
		return nil
	}
	var transport *option.V2RayTransportOptions
	switch o := options.(type) {
	case *option.VLESSInboundOptions:
		transport = o.Transport
	case *option.VMessInboundOptions:
		transport = o.Transport
	case *option.TrojanInboundOptions:
		transport = o.Transport
	case *option.ShadowsocksInboundOptions:
		if !slices.Contains(o.Network.Build(), "tcp") {
			return E.New("TCP final_mask requires a TCP listener")
		}
	case *option.NaiveInboundOptions:
		if !slices.Contains(o.Network.Build(), "tcp") {
			return E.New("TCP final_mask requires a TCP listener")
		}
	case *option.HTTPInboundOptions:
		if !slices.Contains(o.Versions(), 1) && !slices.Contains(o.Versions(), 2) {
			return E.New("TCP final_mask cannot wrap an HTTP/3-only listener")
		}
	case *option.HTTPMixedInboundOptions, *option.SocksInboundOptions, *option.AnyTLSInboundOptions, *option.ShadowTLSInboundOptions, *option.SnellInboundOptions:

	default:
		return E.New("inbound final_mask requires a supported TCP listener")
	}
	if transport != nil {
		switch transport.Type {
		case "http", "ws", "grpc", "httpupgrade", "xhttp":
		default:
			return E.New("inbound TCP final_mask cannot wrap transport ", transport.Type)
		}
	}
	if tls, ok := options.(option.InboundTLSOptionsWrapper); ok {
		if o := tls.TakeInboundTLSOptions(); o != nil && o.Enabled && o.Reality != nil && o.Reality.Enabled {
			return E.New("inbound TCP final_mask is incompatible with REALITY")
		}
	}
	_, err := finalmask.Build(mask)
	return err
}
