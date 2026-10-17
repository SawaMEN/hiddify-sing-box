// Package olcrtc provides a native sing-box outbound backed by the olcRTC Go
// runtime. The upstream signaling/ICE sockets are protected by Android's
// NetworkManager so that they never recursively enter the VPN's own TUN.
package olcrtc

import (
    "context"
    "encoding/hex"
    "errors"
    "fmt"
    "net"
    "net/netip"
    "sync"
    "syscall"
    "time"

    upstream "github.com/openlibrecommunity/olcrtc/mobile"
    "github.com/sagernet/sing-box/adapter"
    "github.com/sagernet/sing-box/adapter/outbound"
    C "github.com/sagernet/sing-box/constant"
    "github.com/sagernet/sing-box/log"
    "github.com/sagernet/sing-box/option"
    M "github.com/sagernet/sing/common/metadata"
    N "github.com/sagernet/sing/common/network"
    "github.com/sagernet/sing/common/control"
    "github.com/sagernet/sing/protocol/socks"
    "github.com/sagernet/sing/service"
)

const (
    connectionReadyMS = 30000
    connectionStopMS = 5000
)

func RegisterOutbound(r *outbound.Registry) {
    outbound.Register[option.OlcRTCOutboundOptions](r, C.TypeOlcRTC, NewOutbound)
}

var _ adapter.Outbound = (*Outbound)(nil)

// ProtectFunc implements olcRTC's fd callback. Protect is called before a
// provider, ICE or DNS socket can be used. A failed protection rejects the
// connection instead of silently looping traffic into the VPN.
type ProtectFunc struct { protect control.Func }
func (p ProtectFunc) Protect(fd int) bool {
    if p.protect == nil { return false }
    return p.protect("udp", "", rawFD(uintptr(fd))) == nil
}
type rawFD uintptr
func (fd rawFD) Control(f func(uintptr)) error { f(uintptr(fd)); return nil }
func (rawFD) Read(func(uintptr) bool) error { return errors.New("read unavailable") }
func (rawFD) Write(func(uintptr) bool) error { return errors.New("write unavailable") }
var _ syscall.RawConn = rawFD(0)

type loopbackDialer struct{}
func (loopbackDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
    return (&net.Dialer{}).DialContext(ctx, network, destination.String())
}
func (loopbackDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
    return (&net.ListenConfig{}).ListenPacket(ctx, "udp", "127.0.0.1:0")
}

type Outbound struct {
    outbound.Adapter
    runtime *upstream.Runtime
    context context.Context
    cancel context.CancelFunc
    mutex sync.Mutex
    socks *socks.Client
    closed bool
    provider string
    transport string
}

func validate(o option.OlcRTCOutboundOptions) error {
    switch o.Provider {
    case "jitsi", "telemost", "wbstream", "none":
    default: return errors.New("olcrtc: unsupported provider")
    }
    switch o.Transport {
    case "datachannel", "vp8channel", "seichannel", "videochannel":
    default: return errors.New("olcrtc: unsupported transport")
    }
    if (o.Provider == "telemost" && (o.Transport == "datachannel" || o.Transport == "seichannel")) ||
        (o.Provider == "wbstream" && o.Transport == "datachannel" && o.ProviderToken == "") {
        return errors.New("olcrtc: transport cannot operate with this provider without additional credentials")
    }
    if o.Provider != "none" && (o.RoomID == "" || len(o.RoomID) > 2048) {
        return errors.New("olcrtc: invalid room identifier")
    }
    key, err := hex.DecodeString(o.Key)
    if err != nil || len(key) != 32 {
        return errors.New("olcrtc: key must be 32 bytes encoded as hex")
    }
    if o.DNSServer != "" {
        host, port, err := net.SplitHostPort(o.DNSServer)
        if err != nil || host == "" || port == "" { return errors.New("olcrtc: invalid DNS server") }
    }
    return nil
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options option.OlcRTCOutboundOptions) (adapter.Outbound, error) {
    if err := validate(options); err != nil { return nil, err }
    runtime := upstream.New()
    if err := runtime.SetProvider(options.Provider); err != nil { return nil, err }
    if err := runtime.SetTransport(options.Transport); err != nil { return nil, err }
    if options.Provider != "none" {
        if err := runtime.SetRoom(options.RoomID); err != nil { return nil, err }
    }
    if err := runtime.SetKey(options.Key); err != nil { return nil, err }
    dnsServer := options.DNSServer
    if dnsServer == "" { dnsServer = "8.8.8.8:53" }
    if err := runtime.SetDNS(dnsServer); err != nil { return nil, err }
    if err := runtime.SetSocksListenHost("127.0.0.1"); err != nil { return nil, err }
    runtime.SetProviderToken(options.ProviderToken)
    switch options.Transport {
    case "vp8channel":
        fps, batch := 30, 64
        if options.VP8FPS > 0 { fps = options.VP8FPS }
        if options.VP8Batch > 0 { batch = options.VP8Batch }
        if err := runtime.SetVP8Options(fps, batch); err != nil { return nil, err }
    case "seichannel":
        fps, batch, frag, ack := 30, 64, 900, 2000
        if options.SEIFPS > 0 { fps = options.SEIFPS }
        if options.SEIBatch > 0 { batch = options.SEIBatch }
        if options.SEIFragment > 0 { frag = options.SEIFragment }
        if options.SEIAckTimeout > 0 { ack = options.SEIAckTimeout }
        if err := runtime.SetSEIOptions(fps, batch, frag, ack); err != nil { return nil, err }
    case "videochannel":
        w, h, fps, codec, recovery := 1920, 1080, 30, "qrcode", "low"
        if options.VideoWidth > 0 { w = options.VideoWidth }
        if options.VideoHeight > 0 { h = options.VideoHeight }
        if options.VideoFPS > 0 { fps = options.VideoFPS }
        if options.VideoCodec != "" { codec = options.VideoCodec }
        if options.VideoQRRecovery != "" { recovery = options.VideoQRRecovery }
        tileModule := options.VideoTileModule
        if tileModule == 0 { tileModule = 4 }
        if err := runtime.SetVideoOptions(w, h, fps, options.VideoQRSize, recovery, codec, tileModule, options.VideoTileRS); err != nil { return nil, err }
    }

    network := service.FromContext[adapter.NetworkManager](ctx)
    if network != nil {
        protect := network.ProtectFunc()
        if protect == nil && network.AutoDetectInterface() { protect = network.AutoDetectInterfaceFunc() }
        if protect != nil {
            runtime.SetProtector(ProtectFunc{protect: protect})
        }
    }
    life, cancel := context.WithCancel(ctx)
    return &Outbound{
        Adapter: outbound.NewAdapter(C.TypeOlcRTC, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
        runtime: runtime, context: life, cancel: cancel,
        provider: options.Provider, transport: options.Transport,
    }, nil
}

// startLocked does not wait for a remote peer: unavailable WebRTC relays must not
// block VPN startup or prevent a fallback outbound from being chosen.
func (h *Outbound) startLocked() error {
    if h.closed { return net.ErrClosed }
    if h.runtime.IsRunning() { return nil }
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil { return fmt.Errorf("olcrtc: local SOCKS port allocation failed: %w", err) }
    port := listener.Addr().(*net.TCPAddr).Port
    // The upstream runtime owns its listener. Close the reservation immediately
    // before starting; a collision is reported as a startup error by the runtime.
    _ = listener.Close()
    if err := h.runtime.SetSocksPort(port); err != nil { return err }
    if err := h.runtime.Start(); err != nil { return fmt.Errorf("olcrtc: startup failed: %w", err) }
    h.socks = socks.NewClient(loopbackDialer{}, M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), uint16(port)), socks.Version5, "", "")
    return nil
}

func (h *Outbound) Start(stage adapter.StartStage) error {
    if stage != adapter.StartStateStart { return nil }
    h.mutex.Lock()
    defer h.mutex.Unlock()
    return h.startLocked()
}
func (h *Outbound) ensure(ctx context.Context) (*socks.Client, error) {
    h.mutex.Lock()
    if err := h.startLocked(); err != nil {
        h.mutex.Unlock()
        return nil, err
    }
    client := h.socks
    h.mutex.Unlock()

    // WaitReady is bounded; the runtime's readiness means its local SOCKS
    // listener has been opened (not that remote traffic is guaranteed).
    ready := make(chan error, 1)
    go func() { ready <- h.runtime.WaitReady(connectionReadyMS) }()
    select {
    case err := <-ready:
        if err != nil { return nil, fmt.Errorf("olcrtc: relay not ready: %w", err) }
    case <-ctx.Done(): return nil, ctx.Err()
    case <-h.context.Done(): return nil, net.ErrClosed
    }
    return client, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
    client, err := h.ensure(ctx)
    if err != nil { return nil, err }
    return client.DialContext(ctx, network, destination)
}
func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
    client, err := h.ensure(ctx)
    if err != nil { return nil, err }
    return client.ListenPacket(ctx, destination)
}
func (h *Outbound) Close() error {
    h.cancel()
    h.mutex.Lock()
    defer h.mutex.Unlock()
    if h.closed { return nil }
    h.closed = true
    h.socks = nil
    return h.runtime.Stop(connectionStopMS)
}

// Explicitly retain the compile-time context dependency for Go versions before
// Go 1.26's structured asynchronous cancellation API.
var _ = time.Second
