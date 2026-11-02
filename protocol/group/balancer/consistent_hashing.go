package balancer

import (
	"math"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/contrab/maphash"
)

type ConsistentHashing struct {
	outbounds            map[string][]adapter.Outbound
	delays               map[string]uint16
	hash                 maphash.Hasher[string]
	maxRetry             int
	maxAcceptableDelay   map[string]uint16
	mu                   sync.Mutex
	delayAcceptableRatio float64
}

func NewConsistentHashing(outbounds []adapter.Outbound, options option.BalancerOutboundOptions) *ConsistentHashing {
	cOutbounds := convertOutbounds(outbounds)

	return &ConsistentHashing{
		outbounds:            cOutbounds,
		hash:                 maphash.NewHasher[string](),
		maxRetry:             options.MaxRetry,
		delayAcceptableRatio: options.DelayAcceptableRatio,
	}
}

var _ Strategy = (*ConsistentHashing)(nil)

func (receiver *ConsistentHashing) Now() string {
	return ""
}

func (receiver *ConsistentHashing) UpdateOutboundsInfo(history map[string]*adapter.URLTestHistory) bool {
	_, minDelay := getMinDelay(receiver.outbounds, history)
	delayMap := getDelayMap(history)
	res := map[string]uint16{}
	for net, d := range minDelay {
		acceptableDelay := uint16(math.Min(65535, math.Max(100, float64(d))*receiver.delayAcceptableRatio))
		res[net] = acceptableDelay
	}
	receiver.mu.Lock()
	receiver.delays = delayMap
	receiver.maxAcceptableDelay = res
	receiver.mu.Unlock()
	return true
}

func (receiver *ConsistentHashing) Select(metadata adapter.InboundContext, net string, touch bool) adapter.Outbound {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if net != N.NetworkTCP && net != N.NetworkUDP {
		net = N.NetworkTCP
	}
	key := receiver.hash.Hash(getKey(&metadata))
	buckets := int32(len(receiver.outbounds[net]))
	for i := 0; i < receiver.maxRetry; i, key = i+1, key+1 {
		idx := jumpHash(key, buckets)
		proxy := receiver.outbounds[net][idx]
		if receiver.Alive(proxy, net) {
			return proxy
		}
	}

	// when availability is poor, traverse the entire list to get the available nodes
	for _, proxy := range receiver.outbounds[net] {
		if receiver.Alive(proxy, net) {
			return proxy
		}
	}
	idx := jumpHash(key, buckets)
	return receiver.outbounds[net][idx]
}

func (receiver *ConsistentHashing) Alive(proxy adapter.Outbound, net string) bool {
	if delay, ok := receiver.delays[proxy.Tag()]; ok {
		return delay <= receiver.maxAcceptableDelay[net]
	}
	return false
}
