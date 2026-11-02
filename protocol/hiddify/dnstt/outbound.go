package dnstt

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	"github.com/sagernet/sing/service"

	multidns "github.com/hiddify/hmrd_multi_resolver_dns"
	dnstt "github.com/net2share/vaydns/client"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.DnsttOptions](registry, C.TypeDNSTT, NewOutbound)
	loadResolvers()
}

var _ adapter.Outbound = (*Outbound)(nil)

type Outbound struct {
	outbound.Adapter
	logger logger.ContextLogger
	ctx    context.Context

	candidateResolvers []ResolverS
	resolvers          []dnstt.Resolver
	tunnels            []*dnstt.Tunnel
	mutlitunnel        *dnstt.Tunnel
	mu                 sync.Mutex
	cache              adapter.CacheFile
	uotClient          *uot.Client
	started            int

	mdMgr *multidns.Manager // smart pool, set when options.SmartPool

	options option.DnsttOptions
}

func (receiver *Outbound) PreStart() error {
	receiver.cache = service.FromContext[adapter.CacheFile](receiver.ctx)
	return nil
}

func (receiver *Outbound) PostStart() error {
	go receiver.startTestResolver()
	return nil
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DnsttOptions) (adapter.Outbound, error) {
	// if options.TunnelPerResolver <= 0 {
	// 	options.TunnelPerResolver = 4
	// }

	resolvers, err := getConfigResolvers(options)
	if err != nil {
		return nil, err
	}

	if len(resolvers) == 0 {
		return nil, E.New("at least one resolver is required")
	}
	if options.RecordType == "" {
		options.RecordType = "txt"
	}
	if options.PublicKey == "" {
		return nil, E.New("public key is required")
	}

	if options.Domain == "" {
		return nil, E.New("domain is required")
	}
	if options.PreTestRecordType == "" {
		options.PreTestRecordType = "a"
	}
	if options.PreTestDomain == "" {
		options.PreTestDomain = "www.google.com"
	}

	out := &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeDNSTT, tag, []string{N.NetworkTCP}, options.DialerOptions),
		ctx:     ctx,
		logger:  logger,

		candidateResolvers: resolvers,
		resolvers:          make([]dnstt.Resolver, 0),
		tunnels:            make([]*dnstt.Tunnel, 0),

		options: options,
	}

	if options.SmartPool {
		mgr, addr, err := multidns.StartLocal(multidns.Options{})
		if err != nil {
			return nil, err
		}
		out.mdMgr = mgr
		out.resolvers = []dnstt.Resolver{{ResolverType: dnstt.ResolverTypeUDP, ResolverAddr: addr}}
		out.tunnels = []*dnstt.Tunnel{nil}
		logger.InfoContext(ctx, "dnstt smart_pool listening on ", addr)
	}

	return out, nil
}

func (receiver *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if !receiver.IsReady() {
		return nil, E.New("outbound is not started")
	}
	if receiver.options.SingleResolver {
		return receiver.OpenStreamSingleResolver(ctx)
	}
	return receiver.OpenStream(ctx)
}

func (receiver *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if !receiver.IsReady() {
		return nil, E.New("outbound is not started")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = receiver.Tag()
	metadata.Destination = destination
	if receiver.uotClient != nil {
		receiver.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
		return receiver.uotClient.ListenPacket(ctx, destination)
	}
	return nil, E.New("UoT is not enabled for this outbound")
}

func (receiver *Outbound) IsReady() bool {
	return receiver.started > 0
}

func (receiver *Outbound) DisplayType() string {
	str := C.ProxyDisplayName(receiver.Type())
	if receiver.started == 0 {
		str += " ⚠️ Connecting..."
	} else if receiver.started < 0 {
		str += " ❌ Failed!"
	} else {
		str += fmt.Sprint(" ✔️ ", len(receiver.resolvers), " resolvers")
	}
	return str
}

func (receiver *Outbound) Close() error {
	for _, t := range receiver.tunnels {
		if t != nil {
			t.Close()
		}
	}
	if receiver.mdMgr != nil {
		_ = receiver.mdMgr.Close()
	}
	return nil
}
