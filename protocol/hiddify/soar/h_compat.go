package soar

import "github.com/sagernet/sing-box/adapter"

func (h *Outbound) IsReady() bool { return h.Status() == adapter.StatusReady }
