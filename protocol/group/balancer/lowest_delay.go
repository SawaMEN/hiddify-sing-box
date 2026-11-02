package balancer

import (
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/monitoring"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
)

type LowestDelay struct {
	outbounds        map[string][]adapter.Outbound
	selectedOutbound map[string]adapter.Outbound
	tolerance        uint16
	toleranceRatio   float64
	mu               sync.Mutex
}

func NewLowestDelay(outbounds []adapter.Outbound, options option.BalancerOutboundOptions) *LowestDelay {
	available := convertOutbounds(outbounds)
	tolerance := options.Tolerance
	if tolerance == 0 {
		tolerance = defaultTolerance
	}
	ratio := options.ToleranceRatio
	if ratio <= 0 {
		ratio = defaultToleranceRatio
	}
	return &LowestDelay{
		outbounds: available,
		selectedOutbound: map[string]adapter.Outbound{
			N.NetworkUDP: available[N.NetworkUDP][0],
			N.NetworkTCP: available[N.NetworkTCP][0],
		},
		tolerance:      tolerance,
		toleranceRatio: ratio,
	}
}

var _ Strategy = (*LowestDelay)(nil)

func (s *LowestDelay) Now() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if selected := s.selectedOutbound[N.NetworkTCP]; selected != nil {
		return selected.Tag()
	}
	return ""
}

func (s *LowestDelay) UpdateOutboundsInfo(history map[string]*adapter.URLTestHistory) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for network, available := range s.outbounds {
		selected := s.selectedOutbound[network]
		best := selected
		bestDelay := monitoring.TimeoutDelay
		for _, candidate := range available {
			delay := getTagDelay(candidate.Tag(), history)
			if delay < bestDelay {
				bestDelay, best = delay, candidate
			}
		}
		if selected == nil {
			if best != nil {
				s.selectedOutbound[network] = best
				changed = true
			}
			continue
		}
		if best == nil || best.Tag() == selected.Tag() || bestDelay >= monitoring.TimeoutDelay {
			continue
		}
		currentDelay := getTagDelay(selected.Tag(), history)
		if currentDelay >= monitoring.TimeoutDelay ||
			uint32(currentDelay) > uint32(bestDelay)+switchThreshold(measuredDelay(history[selected.Tag()]), s.tolerance, s.toleranceRatio) {
			s.selectedOutbound[network] = best
			changed = true
		}
	}
	return changed
}

func (s *LowestDelay) Select(metadata adapter.InboundContext, network string, touch bool) adapter.Outbound {
	s.mu.Lock()
	defer s.mu.Unlock()
	if network != N.NetworkTCP && network != N.NetworkUDP {
		network = N.NetworkTCP
	}
	return s.selectedOutbound[network]
}
