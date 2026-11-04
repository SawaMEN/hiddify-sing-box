package warp

import "github.com/sagernet/sing-box/adapter"

// IsReady retains the fork adapter contract alongside detailed Status.
func (w *WARPEndpoint) IsReady() bool { return w.Status() == adapter.StatusReady }
