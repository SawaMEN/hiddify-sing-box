package pingtunnel

import (
	"context"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	p "github.com/sagernet/sing-box/transport/compat/pingtunnel"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/service"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"syscall"
)

func RegisterOutbound(r *outbound.Registry) { outbound.Register(r, "pingtunnel", NewOutbound) }

type Outbound struct {
	packetListen func(string) (*p.PacketConn, error)
	outbound.Adapter
	ctx     context.Context
	cancel  context.CancelFunc
	opts    option.PingTunnelOutboundOptions
	client  *p.Client
	socks   *socks.Client
	mu      sync.Mutex
	closed  bool
	dns     adapter.DNSRouter
	protect control.Func
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, o option.PingTunnelOutboundOptions) (adapter.Outbound, error) {
	if strings.TrimSpace(o.Server) == "" || strings.ContainsAny(o.Server, "\r\n\x00 /:") {
		return nil, fmt.Errorf("pingtunnel: IPv4 server host is required")
	}
	if o.Key < 0 || int64(o.Key) > 2147483647 {
		return nil, fmt.Errorf("pingtunnel: key must fit a nonnegative int32")
	}
	if o.Detour != "" || o.BindInterface != "" || o.Inet4BindAddress != nil || o.Inet6BindAddress != nil {
		return nil, fmt.Errorf("pingtunnel: TCP/UDP detours and bind settings cannot carry ICMP")
	}
	if _, e := p.ParseEncryptionMode(o.Encryption); e != nil && o.Encryption != "" {
		return nil, fmt.Errorf("pingtunnel: unsupported encryption mode")
	}
	life, cancel := context.WithCancel(ctx)
	h := &Outbound{Adapter: outbound.NewAdapterWithDialerOptions("pingtunnel", tag, []string{N.NetworkTCP, N.NetworkUDP}, o.DialerOptions), ctx: life, cancel: cancel, opts: o, dns: service.FromContext[adapter.DNSRouter](ctx)}
	if nm := service.FromContext[adapter.NetworkManager](ctx); nm != nil {
		h.protect = nm.ProtectFunc()
		if h.protect == nil && nm.AutoDetectInterface() {
			h.protect = nm.AutoDetectInterfaceFunc()
		}
	}
	return h, nil
}

type rawControl uintptr

func (r rawControl) Control(f func(uintptr)) error  { f(uintptr(r)); return nil }
func (r rawControl) Read(func(uintptr) bool) error  { return fmt.Errorf("unsupported") }
func (r rawControl) Write(func(uintptr) bool) error { return fmt.Errorf("unsupported") }

var _ syscall.RawConn = rawControl(0)

func (h *Outbound) ensure(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return net.ErrClosed
	}
	if h.client != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	server := h.opts.Server
	ip, e := netip.ParseAddr(server)
	if e != nil {
		if h.dns == nil {
			return fmt.Errorf("pingtunnel: DNS router unavailable")
		}
		addresses, e := h.dns.Lookup(ctx, server, adapter.DNSQueryOptions{})
		if e != nil {
			return e
		}
		for _, a := range addresses {
			if a.Is4() {
				ip = a
				break
			}
		}
	}
	if !ip.IsValid() || !ip.Is4() {
		return fmt.Errorf("pingtunnel: server requires an IPv4 address")
	}
	// Keep ownership of the port until the client takes over the listener.
	l, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		return e
	}
	addr := l.Addr().String()
	owned := false
	defer func() {
		if !owned {
			l.Close()
		}
	}()
	var crypto *p.CryptoConfig
	if h.opts.Encryption != "" && h.opts.Encryption != "none" {
		mode, e := p.ParseEncryptionMode(h.opts.Encryption)
		if e != nil {
			return e
		}
		crypto, e = p.NewCryptoConfig(mode, h.opts.EncryptionKey)
		if e != nil {
			return fmt.Errorf("pingtunnel: invalid encryption key")
		}
	}
	c, e := p.NewClient(addr, ip.String(), "", 60, h.opts.Key, "0.0.0.0", 1, 1<<20, 100, 400, 0, 0, 1, 1024, crypto, "", "", "bb")
	if e != nil {
		return fmt.Errorf("pingtunnel: client initialization failed")
	}
	c.TCPListener = l
	c.PacketListen = h.packetListen
	if h.protect != nil {
		c.SocketControl = func(fd uintptr) error { return h.protect("ip4:icmp", ip.String(), rawControl(fd)) }
	}
	if e = c.Run(); e != nil {
		return fmt.Errorf("pingtunnel: ICMP startup failed: %w", e)
	}
	owned = true
	h.client = c
	h.socks = socks.NewClient(loopbackDialer{}, M.ParseSocksaddr(addr), socks.Version5, "", "")
	return nil
}

type loopbackDialer struct{}

func (loopbackDialer) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, n, d.String())
}
func (loopbackDialer) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	return (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
}
func (h *Outbound) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	if e := h.ensure(ctx); e != nil {
		return nil, e
	}
	if N.NetworkName(n) == N.NetworkUDP {
		p, e := h.socks.ListenPacket(ctx, M.SocksaddrFrom(netip.IPv4Unspecified(), 0))
		if e != nil {
			return nil, e
		}
		return &packetStream{PacketConn: &socksPackets{p}, remote: d}, nil
	}
	return h.socks.DialContext(ctx, n, d)
}
func (h *Outbound) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	if e := h.ensure(ctx); e != nil {
		return nil, e
	}
	p, e := h.socks.ListenPacket(ctx, M.SocksaddrFrom(netip.IPv4Unspecified(), 0))
	if e != nil {
		return nil, e
	}
	return &socksPackets{p}, nil
}
func (h *Outbound) Close() error {
	h.cancel()
	h.mu.Lock()
	h.closed = true
	c := h.client
	h.client = nil
	h.mu.Unlock()
	if c != nil {
		c.Stop()
	}
	return nil
}

type packetStream struct {
	net.PacketConn
	remote net.Addr
}

func (p *packetStream) Read(b []byte) (int, error)  { n, _, e := p.ReadFrom(b); return n, e }
func (p *packetStream) Write(b []byte) (int, error) { return p.WriteTo(b, p.remote) }
func (p *packetStream) RemoteAddr() net.Addr        { return p.remote }

type socksPackets struct{ net.PacketConn }

func (p *socksPackets) ReadFrom(b []byte) (int, net.Addr, error) {
	raw := make([]byte, len(b)+262)
	n, a, e := p.PacketConn.ReadFrom(raw)
	if e != nil {
		return 0, nil, e
	}
	if n > len(b) {
		return 0, nil, io.ErrShortBuffer
	}
	return copy(b, raw[:n]), a, nil
}
func (p *socksPackets) WriteTo(b []byte, a net.Addr) (int, error) {
	_, e := p.PacketConn.WriteTo(b, a)
	if e != nil {
		return 0, e
	}
	return len(b), nil
}
