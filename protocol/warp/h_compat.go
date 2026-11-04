package warp

import "github.com/sagernet/sing-box/adapter"

// IsReady retains the fork adapter contract alongside detailed Status.
func (o *WARPEndpoint) IsReady() bool { return o.Status() == adapter.StatusReady }
