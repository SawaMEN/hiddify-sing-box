package transport

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/ameshkov/dnscrypt/v2"
	mDNS "github.com/miekg/dns"
)

var _ adapter.DNSTransport = (*SDNSTransport)(nil)

func RegisterSDNS(registry *dns.TransportRegistry) {
	dns.RegisterTransport[option.SDNSDNSServerOptions](registry, C.DNSTypeSDNS, NewSDNSTransport)
}

type SDNSTransport struct {
	dns.TransportAdapter
	client *dnscrypt.Client
	name   string
	stamp  string
}

func NewSDNSTransport(ctx context.Context, logger log.ContextLogger, tag string, options option.SDNSDNSServerOptions) (adapter.DNSTransport, error) {
	transportDialer, err := dns.NewRemoteDialer(ctx, options.RemoteDNSServerOptions)
	if err != nil {
		return nil, err
	}
	return &SDNSTransport{
		TransportAdapter: dns.NewTransportAdapterWithRemoteOptions(C.DNSTypeSDNS, tag, options.RemoteDNSServerOptions),
		name:             tag,
		client: &dnscrypt.Client{
			Net:     "udp",
			Timeout: 10 * time.Second,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return hSDNSDialPacket(ctx, transportDialer, network, addr) // H
			},
		},
		stamp: options.Stamp,
	}, err
}

func (t *SDNSTransport) Name() string {
	return t.name
}

func (t *SDNSTransport) Start(adapter.StartStage, *adapter.Scope) error {
	return nil
}

func (t *SDNSTransport) Close() error {
	return nil
}

func (t *SDNSTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(response *mDNS.Msg, err error)) {
	callback(t.Exchange(ctx, message))
}

func (t *SDNSTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	resolverInfo, err := t.client.Dial(t.stamp)
	if err != nil {
		return nil, err
	}
	return t.client.Exchange(message, resolverInfo)
}

func (t *SDNSTransport) Reset() {
}
