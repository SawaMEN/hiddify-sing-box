package masque

import "github.com/sagernet/sing-box/adapter"

// IsReady retains the fork adapter contract alongside detailed Status.
func (w *Outbound) IsReady() bool { return w.Status() == adapter.StatusReady }
