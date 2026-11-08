package option

import (
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
)

type SelectorOutboundOptions struct {
	Balancer                  *XrayBalancerOptions `json:"balancer,omitempty"`
	Outbounds                 []string             `json:"outbounds" reference:"outbound"`
	Default                   string               `json:"default,omitempty" reference:"outbound"`
	InterruptExistConnections bool                 `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds" reference:"outbound"`
	URL                       string             `json:"url,omitempty"`
	URLs                      []string           `json:"urls,omitempty"` // H
	Mode                      string             `json:"mode,omitempty"` // H
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

type XrayBalancerOptions struct {
	Burst        bool               `json:"burst,omitempty"`
	Concurrent   bool               `json:"concurrent,omitempty"`
	Connectivity string             `json:"connectivity,omitempty"`
	Strategy     string             `json:"strategy"`
	Fallback     string             `json:"fallback,omitempty" reference:"outbound"`
	Settings     json.RawMessage    `json:"settings,omitempty"`
	ProbeURL     string             `json:"probe_url,omitempty"`
	ProbeTags    []string           `json:"probe_tags,omitempty"`
	Interval     badoption.Duration `json:"interval,omitempty"`
	Timeout      badoption.Duration `json:"timeout,omitempty"`
	Sampling     uint16             `json:"sampling,omitempty"`
	Method       string             `json:"method,omitempty"`
}
