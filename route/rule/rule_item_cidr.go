package rule

import (
	"net/netip"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/ipset"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"

	"go4.org/netipx"
)

var _ RuleItem = (*IPCIDRItem)(nil)

type IPCIDRItem struct {
	ipSet       *ipset.Set
	isSource    bool
	description string
}

func NewIPCIDRItem(isSource bool, prefixables []string) (*IPCIDRItem, error) {
	var builder netipx.IPSetBuilder
	for _, value := range prefixables {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			addr, addrErr := netip.ParseAddr(value)
			if addrErr != nil {
				return nil, E.Cause(err, "invalid IP CIDR: ", value)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		builder.AddPrefix(prefix.Masked())
	}
	var description string
	if isSource {
		description = "source_ip_cidr="
	} else {
		description = "ip_cidr="
	}
	prefixString := func(it string) string {
		return it
	}
	if prefixCount := len(prefixables); prefixCount == 1 {
		description += prefixString(prefixables[0])
	} else if prefixCount > 3 {
		description += "[" + strings.Join(common.Map(prefixables[:3], prefixString), " ") + "...]"
	} else {
		description += "[" + strings.Join(common.Map(prefixables, prefixString), " ") + "]"
	}
	ipSet, err := builder.IPSet()
	if err != nil {
		return nil, err
	}
	return &IPCIDRItem{
		ipSet:       ipset.FromIPSet(ipSet),
		isSource:    isSource,
		description: description,
	}, nil
}

func NewRawIPCIDRItem(isSource bool, ipSet *ipset.Set) *IPCIDRItem {
	var description string
	if isSource {
		description = "source_ip_cidr="
	} else {
		description = "ip_cidr="
	}
	description += "<binary>"
	return &IPCIDRItem{
		ipSet:       ipSet,
		isSource:    isSource,
		description: description,
	}
}

func (r *IPCIDRItem) Match(metadata *adapter.InboundContext) bool {
	if r.isSource || metadata.IPCIDRMatchSource {
		return r.ipSet.Contains(metadata.Source.Addr)
	}
	if metadata.DestinationAddressMatchFromResponse {
		addresses := metadata.DNSResponseAddressesForMatch()
		if len(addresses) == 0 {
			// Legacy rule_set_ip_cidr_accept_empty only applies when the DNS response
			// does not expose any address answers for matching.
			return metadata.IPCIDRAcceptEmpty
		}
		for _, address := range addresses {
			if r.ipSet.Contains(address) {
				return true
			}
		}
		return false
	}
	if metadata.Destination.IsIP() {
		return r.ipSet.Contains(metadata.Destination.Addr)
	}
	addresses := metadata.DestinationAddresses
	if len(addresses) > 0 {
		for _, address := range addresses {
			if r.ipSet.Contains(address) {
				return true
			}
		}
		return false
	}
	return metadata.IPCIDRAcceptEmpty
}

func (r *IPCIDRItem) String() string {
	return r.description
}
