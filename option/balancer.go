package option

import "github.com/sagernet/sing/common/json/badoption"

type BalancerOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	ToleranceRatio            float64            `json:"tolerance_ratio,omitempty"` // H
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
	Strategy                  string             `json:"strategy,omitempty"`
	DelayAcceptableRatio      float64            `json:"delay_acceptable_ratio,omitempty"`
	TTL                       badoption.Duration `json:"ttl,omitempty"`
	MaxRetry                  int                `json:"max_retry,omitempty"`      // not implemented yet
	Pool                      int                `json:"pool,omitempty"`           // H
	PoolTolerance             uint16             `json:"pool_tolerance,omitempty"` // H
	StickyHash                []string           `json:"sticky_hash,omitempty"`    // H
}
