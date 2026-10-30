package vetroff

import (
    "context"
    "errors"
    "net"
    "strconv"
    "sync"
    "time"

    sdk "github.com/SawaMEN/VetroffProxy"
    "github.com/sagernet/sing-box/adapter"
    "github.com/sagernet/sing-box/adapter/outbound"
    C "github.com/sagernet/sing-box/constant"
    "github.com/sagernet/sing-box/log"
    "github.com/sagernet/sing-box/option"
    M "github.com/sagernet/sing/common/metadata"
    N "github.com/sagernet/sing/common/network"
)

// RegisterOutbound adds the in-process VetrOFFProxy client. No executable,
// localhost SOCKS listener, private files or JNI library are required.
func RegisterOutbound(registry *outbound.Registry) {
    outbound.Register[option.VetrOFFOutboundOptions](registry, C.TypeVetrOFF, NewOutbound)
}

var _ adapter.Outbound = (*Outbound)(nil)

type Outbound struct {
    outbound.Adapter
    client *sdk.Client
    ctx context.Context
    cancel context.CancelFunc
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options option.VetrOFFOutboundOptions) (adapter.Outbound, error) {
    config, err := sdk.ClientConfigFromLink(options.Link)
    if err != nil {
        return nil, errors.New("vetroff: invalid connection profile")
    }
    // VetrOFFProxy manages the remote HTTPS sessions itself; the Android VPN
    // host must exclude its own package from TUN (see VPNService).
    client, err := sdk.NewClient(config)
    if err != nil {
        return nil, err
    }
    networks := []string{N.NetworkTCP}
    if config.EnableUDP {
        networks = append(networks, N.NetworkUDP)
    }
    lifetime, cancel := context.WithCancel(context.Background())
    return &Outbound{
        Adapter: outbound.NewAdapter(C.TypeVetrOFF, tag, networks, nil),
        client: client, ctx: lifetime, cancel: cancel,
    }, nil
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
    if network != N.NetworkTCP {
        return nil, N.ErrUnknownNetwork
    }
    if !destination.IsValid() {
        return nil, errors.New("vetroff: invalid TCP destination")
    }
    return o.client.DialContext(ctx, network, destination.String())
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
    if len(o.Network()) < 2 {
        return nil, errors.New("vetroff: UDP is disabled for this profile")
    }
    // The association belongs to the outbound, not to the caller's setup
    // context, which can be cancelled immediately after ListenPacket returns.
    association, err := o.client.OpenUDP(o.ctx)
    if err != nil {
        return nil, err
    }
    return &packetConn{association: association, ctx: o.ctx}, nil
}

func (o *Outbound) Close() error {
    o.cancel()
    return o.client.Close()
}

type packetConn struct {
    association *sdk.UDPConn
    ctx context.Context
    mu sync.Mutex
    readDeadline time.Time
    writeDeadline time.Time
}

func (p *packetConn) ReadFrom(data []byte) (int, net.Addr, error) {
    p.mu.Lock()
    deadline := p.readDeadline
    p.mu.Unlock()
    ctx := p.ctx
    cancel := func() {}
    if !deadline.IsZero() {
        ctx, cancel = context.WithDeadline(ctx, deadline)
    }
    defer cancel()
    reply, err := p.association.Receive(ctx)
    if err != nil {
        return 0, nil, err
    }
    host, portString, err := net.SplitHostPort(reply.Address)
    if err != nil {
        return 0, nil, errors.New("vetroff: invalid UDP reply address")
    }
    port, err := strconv.Atoi(portString)
    if err != nil {
        return 0, nil, errors.New("vetroff: invalid UDP reply port")
    }
    ip := net.ParseIP(host)
    if ip == nil {
        return 0, nil, errors.New("vetroff: UDP reply must contain an IP address")
    }
    n := copy(data, reply.Payload)
    return n, &net.UDPAddr{IP: ip, Port: port}, nil
}

func (p *packetConn) WriteTo(data []byte, destination net.Addr) (int, error) {
    if destination == nil {
        return 0, errors.New("vetroff: missing UDP destination")
    }
    p.mu.Lock()
    deadline := p.writeDeadline
    p.mu.Unlock()
    ctx := p.ctx
    cancel := func() {}
    if !deadline.IsZero() {
        ctx, cancel = context.WithDeadline(ctx, deadline)
    }
    defer cancel()
    // Bound the queued datagram independently of the caller's buffer lifetime.
    payload := append([]byte(nil), data...)
    if err := p.association.Send(ctx, sdk.Datagram{Address: destination.String(), Payload: payload}); err != nil {
        return 0, err
    }
    return len(data), nil
}

func (p *packetConn) Close() error { return p.association.Close() }
func (p *packetConn) LocalAddr() net.Addr { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (p *packetConn) SetDeadline(t time.Time) error {
    p.mu.Lock()
    p.readDeadline, p.writeDeadline = t, t
    p.mu.Unlock()
    return nil
}
func (p *packetConn) SetReadDeadline(t time.Time) error {
    p.mu.Lock()
    p.readDeadline = t
    p.mu.Unlock()
    return nil
}
func (p *packetConn) SetWriteDeadline(t time.Time) error {
    p.mu.Lock()
    p.writeDeadline = t
    p.mu.Unlock()
    return nil
}
