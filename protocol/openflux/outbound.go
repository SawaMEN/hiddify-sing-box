package openflux

import (
	"context"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	t "github.com/sagernet/sing-box/transport/compat/openflux/transport"
	"github.com/sagernet/sing-box/transport/compat/openflux/transport/mailru"
	"github.com/sagernet/sing-box/transport/compat/openflux/transport/manager"
	"github.com/sagernet/sing-box/transport/compat/openflux/transport/yandex"
	"github.com/sagernet/sing-box/transport/iptunnel"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"net"
	"net/netip"
	"net/url"
	"sync"
	"time"
)

func RegisterOutbound(r *outbound.Registry) { outbound.Register(r, "openflux", NewOutbound) }

type Outbound struct {
	outbound.Adapter
	ctx     context.Context
	cancel  context.CancelFunc
	manager *manager.Manager
	network *iptunnel.Network
	once    sync.Once
	dns     adapter.DNSRouter
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, o option.OpenFluxOutboundOptions) (adapter.Outbound, error) {
	if len([]rune(o.Secret)) < 32 || len(o.Transports) == 0 || len(o.Transports) > 16 {
		return nil, fmt.Errorf("openflux: a secret of at least 32 characters and 1–16 transports are required")
	}
	if o.Codec != "" && o.Codec != "batched" {
		return nil, fmt.Errorf("openflux: unsupported codec")
	}

	d, err := dialer.New(ctx, o.DialerOptions, true)
	if err != nil {
		return nil, err
	}
	return newOutboundWithDialer(ctx, tag, o, d)
}
func newOutboundWithDialer(ctx context.Context, tag string, o option.OpenFluxOutboundOptions, d N.Dialer) (result adapter.Outbound, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	ctxOwner := ctx
	dialFn := func(ctx context.Context, n, a string) (net.Conn, error) {
		dialCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		stopLife := context.AfterFunc(ctxOwner, stop)
		defer stopLife()
		return d.DialContext(dialCtx, n, M.ParseSocksaddr(a))
	}
	sources := make([]t.ContextSource, len(o.Transports))
	for i, s := range o.Transports {
		sources[i] = t.ContextSource{Type: s.Type, URL: s.URL, Priority: s.Priority}
	}
	contextKey, alternates := t.KDFContexts(o.Context, "", sources)
	sess, err := t.NewSession(t.PeerParameters{Capabilities: t.CapabilityIPv4 | t.CapabilityTCP | t.CapabilityUDP | t.CapabilityICMPErrors, MaxPacketSize: t.MaxNegotiatedPacket}, false)
	if err != nil {
		return nil, err
	}
	if !o.Negotiate || len(o.Transports) == 1 {
		sess.SetClassic(t.CodecBatched)
	}
	sess.SetAlternateContexts(alternates)
	mgr := manager.New(sess, nil, o.Secret, contextKey)
	for _, s := range o.Transports {
		var raw t.Transport
		config := t.DefaultConfig()
		config.DialContext = dialFn
		switch s.Type {
		case "direct":
			a := M.ParseSocksaddr(s.Dial)
			if !a.IsValid() {
				return nil, fmt.Errorf("openflux: invalid direct endpoint")
			}
			c := t.DefaultDirectConfig()
			c.DialAddr = s.Dial
			raw = t.NewDirectTransport(config, c)
		case "yandex", "vyandex", "boards", "mailru":
			u, e := url.Parse(s.URL)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return nil, fmt.Errorf("openflux: invalid HTTPS channel URL")
			}
			switch s.Type {
			case "yandex":
				raw = yandex.NewYandexDocsTransport(s.URL, config)
			case "vyandex":
				raw = yandex.NewYandexVolgaTransport(s.URL, config)
			case "boards":
				raw = yandex.NewBoardsTransport(s.URL, config)
			case "mailru":
				raw = mailru.NewMailruDocsTransport(s.URL, config)
			}
		default:
			return nil, fmt.Errorf("openflux: unsupported carrier type")
		}
		if err = sess.AddTransport(s.Type, raw, o.Secret, contextKey, s.Priority); err != nil {
			return nil, err
		}
		provider, _ := raw.(manager.CookieProvider)
		if err = mgr.Add(s.Type, s.Type, raw, s.Priority, provider); err != nil {
			return nil, err
		}
		mgr.SetURL(s.Type, s.URL)
	}
	sess.SetControlHandler(mgr.DispatchControl)
	h := &Outbound{Adapter: outbound.NewAdapterWithDialerOptions("openflux", tag, []string{N.NetworkTCP, N.NetworkUDP}, o.DialerOptions), ctx: ctx, cancel: cancel, manager: mgr, dns: service.FromContext[adapter.DNSRouter](ctx)}
	return h, nil
}
func (h *Outbound) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	n, e := iptunnel.New([]netip.Addr{netip.MustParseAddr("10.10.10.2")}, 1500, h.manager.Send)
	if e != nil {
		return e
	}
	n.Resolve = h.resolve
	h.network = n
	h.manager.Receive(func(b []byte) { n.Inject(b) })
	if e = h.manager.Start(); e != nil {
		n.Close()
		return e
	}
	return nil
}
func (h *Outbound) ready(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		if h.manager.IsConnected() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.ctx.Done():
			return h.ctx.Err()
		case <-timer.C:
			return fmt.Errorf("openflux: session connection timed out")
		case <-ticker.C:
		}
	}
}
func (h *Outbound) resolve(ctx context.Context, d M.Socksaddr) (M.Socksaddr, error) {
	if !d.IsDomain() {
		if d.Addr.Is6() {
			return M.Socksaddr{}, fmt.Errorf("openflux: IPv6 is not supported by upstream 0.3.0")
		}
		return d, nil
	}
	if h.dns == nil {
		return M.Socksaddr{}, fmt.Errorf("openflux: DNS router unavailable")
	}
	a, e := h.dns.Lookup(ctx, d.Fqdn, adapter.DNSQueryOptions{})
	if e != nil {
		return M.Socksaddr{}, e
	}
	for _, ip := range a {
		if ip.Is4() {
			return M.SocksaddrFrom(ip, d.Port), nil
		}
	}
	return M.Socksaddr{}, fmt.Errorf("openflux: IPv4 destination is required")
}
func (h *Outbound) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	if e := h.ready(ctx); e != nil {
		return nil, e
	}
	d, e := h.resolve(ctx, d)
	if e != nil {
		return nil, e
	}
	if d.Addr.Is6() {
		return nil, fmt.Errorf("openflux: IPv6 is not supported by upstream 0.3.0")
	}
	return h.network.DialContext(ctx, n, d)
}
func (h *Outbound) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	if e := h.ready(ctx); e != nil {
		return nil, e
	}
	return h.network.ListenPacket(ctx, d)
}
func (h *Outbound) Close() error {
	var e error
	h.once.Do(func() {
		h.cancel()
		e = h.manager.Stop()
		if h.network != nil {
			h.network.Close()
		}
	})
	return e
}
