//go:build with_kcp

package v2raykcp

import (
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/sagernet/sing-box/transport/v2raykcp/kcp"
	Xbuf "github.com/xtls/xray-core/common/buf"
)

// newConfig builds Xray's mKCP config with Xray's defaults and checks.
func newConfig(options option.V2RayKCPOptions) (*kcp.Config, error) {
	config := &kcp.Config{
		Mtu:              1350,
		Tti:              50,
		UplinkCapacity:   5,
		DownlinkCapacity: 20,
		CwndMultiplier:   1,
		MaxSendingWindow: 2 * 1024 * 1024,
	}
	if options.MTU != 0 {
		config.Mtu = options.MTU
	}
	if options.TTI != 0 {
		config.Tti = options.TTI
	}
	if options.UplinkCapacity != 0 {
		config.UplinkCapacity = options.UplinkCapacity
	}
	if options.DownlinkCapacity != 0 {
		config.DownlinkCapacity = options.DownlinkCapacity
	}
	if options.CwndMultiplier != 0 {
		config.CwndMultiplier = options.CwndMultiplier
	}
	if options.MaxSendingWindow != 0 {
		config.MaxSendingWindow = options.MaxSendingWindow
	}
	if config.Mtu < 21 {
		return nil, E.New("mKCP mtu must be at least 21")
	}
	if config.Tti < 10 || config.Tti > 1000 {
		return nil, E.New("mKCP tti must be between 10 and 1000")
	}
	// Keep the native mKCP transport's guardrails identical to the panel's
	// former transport-balancer.patch. Invalid capacities and windows must
	// fail at startup, not overflow allocation or silently weaken limits.
	if config.Mtu > uint32(Xbuf.Size) {
		return nil, E.New("mKCP mtu exceeds Xray packet buffer")
	}
	if config.CwndMultiplier < 1 {
		return nil, E.New("mKCP cwnd_multiplier must be positive")
	}
	if config.UplinkCapacity > 4095 || config.DownlinkCapacity > 4095 {
		return nil, E.New("mKCP uplink_capacity and downlink_capacity must not exceed 4095")
	}
	if config.MaxSendingWindow < config.Mtu || config.GetSendingBufferSize() == 0 {
		return nil, E.New("mKCP max_sending_window must be at least the mtu")
	}
	return config, nil
}
