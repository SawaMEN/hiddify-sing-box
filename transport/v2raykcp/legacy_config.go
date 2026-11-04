//go:build with_kcp

package v2raykcp

import (
	"encoding/json"
	"fmt"
	"github.com/sagernet/sing-box/hiddify/finalmask"
	"github.com/sagernet/sing-box/option"
	Xbuf "github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport/internet/kcp"
)

func buildOptions(o option.V2RayKCPOptions) (*kcp.Config, *finalmask.Masks, error) {
	c := &kcp.Config{Mtu: 1350, Tti: 50, UplinkCapacity: 5, DownlinkCapacity: 20, CwndMultiplier: 1, MaxSendingWindow: 2 * 1024 * 1024}
	for _, pair := range []struct {
		value uint32
		target *uint32
	}{{o.MTU, &c.Mtu}, {o.TTI, &c.Tti}, {o.UplinkCapacity, &c.UplinkCapacity}, {o.DownlinkCapacity, &c.DownlinkCapacity}, {o.CwndMultiplier, &c.CwndMultiplier}, {o.MaxSendingWindow, &c.MaxSendingWindow}} {
		if pair.value != 0 { *pair.target = pair.value }
	}
	if c.Mtu < 21 || c.Mtu > uint32(Xbuf.Size) || c.Tti < 10 || c.Tti > 1000 || c.CwndMultiplier < 1 || c.MaxSendingWindow < c.Mtu || c.UplinkCapacity > 4095 || c.DownlinkCapacity > 4095 {
		return nil, nil, fmt.Errorf("invalid mKCP MTU, TTI, capacity or sending window")
	}
	masks := o.FinalMask
	if o.Legacy || o.Seed != "" || o.Header != "" && o.Header != "none" {
		if masks != nil && !masks.IsEmpty() {
			return nil, nil, fmt.Errorf("mKCP legacy options and explicit masks are mutually exclusive")
		}
		seed, _ := json.Marshal(map[string]string{"value": o.Seed})
		masks = &option.FinalMaskOptions{UDP: []option.FinalMaskItem{{Type: "mkcp-legacy", Settings: seed}}}
		if o.Header != "" && o.Header != "none" {
			header, _ := json.Marshal(map[string]string{"header": o.Header})
			masks.UDP = append(masks.UDP, option.FinalMaskItem{Type: "mkcp-legacy", Settings: header})
		}
	}
	if masks != nil && len(masks.TCP) > 0 {
		return nil, nil, fmt.Errorf("mKCP accepts UDP masks only")
	}
	built, err := finalmask.Build(masks)
	return c, built, err
}
