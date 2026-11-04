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
	MaxRetry                  int                `json:"max_retry,omitempty"` // consistent-hashing and sticky-sessions: other buckets to try before a full search
	// pool strategy (as lx's round_robin): the number of slots, 3 when 0. A member outside the
	// pool takes a slot when faster by more than tolerance and tolerance_ratio, as for
	// lowest-delay; with tolerance unset the slots keep the first working members.
	Pool          int    `json:"pool,omitempty"`
	PoolTolerance uint16 `json:"pool_tolerance,omitempty"` //H: legacy alias when tolerance is unset //H
	// pool and sticky-sessions: what a connection sticks by: process, domain, source_ip, dest_ip,
	// dest_port, or none. The pool's default is process and domain, sticky-sessions' source_ip
	// and domain.
	StickyHash badoption.Listable[string] `json:"sticky_hash,omitempty"` //H
}
