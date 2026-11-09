package vless

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	SB "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	XR "github.com/xtls/xray-core/app/reverse"
	XC "github.com/xtls/xray-core/common"
	XB "github.com/xtls/xray-core/common/buf"
	XM "github.com/xtls/xray-core/common/mux"
	XN "github.com/xtls/xray-core/common/net"
	XS "github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/routing"
	XT "github.com/xtls/xray-core/transport"
	XP "github.com/xtls/xray-core/transport/pipe"
	"google.golang.org/protobuf/proto"
)

// reverseStream owns the physical stream so mux interruption closes blocking I/O.
type reverseStream struct {
	net.Conn
	reader  XB.Reader
	writer  XB.Writer
	once    sync.Once
	writeMu sync.Mutex
}

func reverseLink(c net.Conn) *XT.Link {
	s := &reverseStream{Conn: c, reader: XB.NewReader(c), writer: XB.NewWriter(c)}
	return &XT.Link{Reader: s, Writer: s}
}

func (s *reverseStream) ReadMultiBuffer() (XB.MultiBuffer, error) { return s.reader.ReadMultiBuffer() }
func (s *reverseStream) WriteMultiBuffer(b XB.MultiBuffer) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.writer.WriteMultiBuffer(b)
}

func (s *reverseStream) Close() error { s.once.Do(func() { s.Conn.Close() }); return nil }
func (s *reverseStream) Interrupt()   { s.Close() }
func xrDestination(d M.Socksaddr, udp bool) XN.Destination {
	a := XN.ParseAddress(d.AddrString())
	if udp {
		return XN.UDPDestination(a, XN.Port(d.Port))
	}
	return XN.TCPDestination(a, XN.Port(d.Port))
}

func sbDestination(d XN.Destination) M.Socksaddr {
	return M.ParseSocksaddrHostPort(d.Address.String(), uint16(d.Port))
}

func reverseContext(ctx context.Context, d XN.Destination) context.Context {
	ctx = XS.ContextWithIsReverseMux(ctx, true)
	return XS.ContextWithOutbounds(ctx, []*XS.Outbound{{Target: d}})
}

type reversePortal struct {
	outbound.Adapter
	ctx     context.Context
	mu      sync.Mutex
	workers []*XM.ClientWorker
	closed  bool
	wg      sync.WaitGroup
}

func NewReversePortal(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ option.VLESSReversePortalOptions) (adapter.Outbound, error) {
	if tag == "" {
		return nil, fmt.Errorf("reverse portal tag required")
	}
	return &reversePortal{Adapter: outbound.NewAdapter("vless-reverse", tag, []string{"tcp", "udp"}, nil), ctx: ctx}, nil
}

func (p *reversePortal) PickAvailable() (*XM.ClientWorker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, net.ErrClosed
	}
	var best *XM.ClientWorker
	active := p.workers[:0]
	for _, w := range p.workers {
		if w.Closed() {
			continue
		}
		active = append(active, w)
		if !w.IsFull() && (best == nil || w.ActiveConnections() < best.ActiveConnections()) {
			best = w
		}
	}
	p.workers = active
	if best == nil {
		return nil, fmt.Errorf("reverse portal %q has no connected bridge", p.Tag())
	}
	return best, nil
}

func (p *reversePortal) accept(ctx context.Context, c net.Conn, onClose N.CloseHandlerFunc) error {
	w, err := XM.NewClientWorker(*reverseLink(c), XM.ClientStrategy{})
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		w.Close()
		return net.ErrClosed
	}
	active := p.workers[:0]
	for _, old := range p.workers {
		if !old.Closed() {
			active = append(active, old)
		}
	}
	p.workers = active
	if len(p.workers) >= 64 {
		p.mu.Unlock()
		w.Close()
		return fmt.Errorf("reverse portal worker limit reached")
	}
	p.workers = append(p.workers, w)
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		defer w.Close()
		defer c.Close()
		defer func() {
			if onClose != nil {
				onClose(nil)
			}
		}()
		stop := context.AfterFunc(ctx, func() { w.Close(); c.Close() })
		defer stop()
		stopRoot := context.AfterFunc(p.ctx, func() { w.Close(); c.Close() })
		defer stopRoot()
		// The reference reverse protocol uses an internal UDP mux session for control.
		upR, upW := XP.New(XP.WithSizeLimit(16384))
		downR, downW := XP.New(XP.WithSizeLimit(16384))
		defer upR.Interrupt()
		defer downR.Interrupt()
		defer upW.Interrupt()
		defer downW.Interrupt()
		if !w.Dispatch(reverseContext(ctx, XN.UDPDestination(XN.DomainAddress("reverse"), 0)), &XT.Link{Reader: upR, Writer: downW}) {
			return
		}
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			ctl := &XR.Control{}
			ctl.FillInRandom()
			data, err := proto.Marshal(ctl)
			if err != nil {
				return
			}
			if err = upW.WriteMultiBuffer(XB.MergeBytes(nil, data)); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-p.ctx.Done():
				return
			case <-w.WaitClosed():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (p *reversePortal) DialContext(ctx context.Context, network string, d M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) == N.NetworkUDP {
		pc, err := p.ListenPacket(ctx, d)
		if err != nil {
			return nil, err
		}
		return newReverseBoundPacket(pc, d), nil
	}
	if N.NetworkName(network) != N.NetworkTCP {
		return nil, N.ErrUnknownNetwork
	}
	if _, err := p.PickAvailable(); err != nil {
		return nil, err
	}
	front, back := net.Pipe()
	stop := context.AfterFunc(ctx, func() { front.Close(); back.Close() })
	go func() {
		defer stop()
		defer back.Close()
		m := XM.ClientManager{Picker: p}
		m.Dispatch(reverseContext(ctx, xrDestination(d, false)), reverseLink(back))
	}()

	return front, nil
}

func (p *reversePortal) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	upR, upW := XP.New(XP.WithSizeLimit(65536))
	downR, downW := XP.New(XP.WithSizeLimit(65536))
	pc := newReversePacket(&XT.Link{Reader: downR, Writer: upW}, d)
	m := XM.ClientManager{Picker: p}
	if err := m.Dispatch(reverseContext(ctx, xrDestination(d, true)), &XT.Link{Reader: upR, Writer: downW}); err != nil {
		pc.Close()
		upR.Interrupt()
		downW.Interrupt()
		return nil, err
	}
	return pc, nil
}

func (p *reversePortal) Close() error {
	p.mu.Lock()
	p.closed = true
	workers := append([]*XM.ClientWorker(nil), p.workers...)
	p.workers = nil
	p.mu.Unlock()
	for _, w := range workers {
		w.Close()
	}
	p.wg.Wait()
	return nil
}

func (h *Inbound) newReverseConnection(ctx context.Context, index int, c net.Conn, source M.Socksaddr, onClose N.CloseHandlerFunc) error {
	user := h.users[index]
	manager := service.FromContext[adapter.OutboundManager](h.ctx)
	if manager == nil {
		return fmt.Errorf("reverse outbound manager unavailable")
	}
	ob, ok := manager.Outbound(user.Reverse.Tag)
	if !ok {
		return fmt.Errorf("reverse portal %q missing", user.Reverse.Tag)
	}
	p, ok := ob.(*reversePortal)
	if !ok {
		return fmt.Errorf("reverse tag does not refer to a reverse portal")
	}
	// Reverse transport bypasses the ordinary router: account for its bytes once.
	if api := service.FromContext[adapter.V2RayServer](h.ctx); api != nil && api.StatsService() != nil {
		c = api.StatsService().RoutedConnection(ctx, c, adapter.InboundContext{Inbound: h.Tag(), InboundType: "vless", User: user.Name, Source: source, Destination: M.Socksaddr{Fqdn: "v1.rvs.cool"}, Network: "tcp"}, nil, p)
	}
	if clash := service.FromContext[adapter.ClashServer](h.ctx); clash != nil {
		c = clash.RoutedConnection(ctx, c, adapter.InboundContext{Inbound: h.Tag(), InboundType: "vless", User: user.Name, Source: source, Destination: M.Socksaddr{Fqdn: "v1.rvs.cool"}, Network: "tcp"}, nil, p)
	}
	return p.accept(ctx, c, onClose)
}

type reverseBridge struct {
	ctx             context.Context
	cancel          context.CancelFunc
	router          adapter.ConnectionRouterEx
	logger          log.ContextLogger
	tag             string
	dialer          *vlessDialer
	mu              sync.Mutex
	started, closed bool
	wg              sync.WaitGroup
}

func newReverseBridge(ctx context.Context, r adapter.Router, l log.ContextLogger, tag string, d *vlessDialer) *reverseBridge {
	ctx, cancel := context.WithCancel(ctx)
	return &reverseBridge{ctx: ctx, cancel: cancel, router: r, logger: l, tag: tag, dialer: d}
}

func (b *reverseBridge) start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started || b.closed {
		return
	}
	b.started = true
	b.wg.Add(1)
	go b.run()
}

func (b *reverseBridge) close() { b.mu.Lock(); b.closed = true; b.cancel(); b.mu.Unlock(); b.wg.Wait() }

type bridgeSession struct {
	worker     *XM.ServerWorker
	dispatcher *reverseDispatcher
	conn       net.Conn
	cancel     context.CancelFunc
}

func (b *reverseBridge) run() {
	defer b.wg.Done()
	var workers []bridgeSession
	defer func() {
		for _, w := range workers {
			w.cancel()
			w.worker.Close()
			w.conn.Close()
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var count, connections uint32
		active := workers[:0]
		for _, w := range workers {
			if w.worker.Closed() {
				w.cancel()
				w.conn.Close()
				continue
			}
			active = append(active, w)
			if !w.dispatcher.draining.Load() {
				count++
				connections += w.worker.ActiveConnections()
			}
		}
		workers = active
		if (count == 0 || connections/count > 16) && len(workers) < 64 {
			ctx, cancel := context.WithCancel(b.ctx)
			timer := time.AfterFunc(15*time.Second, cancel)
			c, err := b.dialer.DialContext(ctx, "tcp", M.Socksaddr{Fqdn: "v1.rvs.cool"})
			if err == nil {
				_, err = c.Write(nil)
			} // Emit VLESS request before waiting for reverse mux.
			if !timer.Stop() && err == nil {
				err = context.DeadlineExceeded
			}
			if err == nil {
				if api := service.FromContext[adapter.V2RayServer](ctx); api != nil {
					if tracker, ok := api.StatsService().(interface {
						RoutedOutboundConnection(context.Context, net.Conn, adapter.Outbound) net.Conn
					}); ok {
						c = tracker.RoutedOutboundConnection(ctx, c, (*Outbound)(b.dialer))
					}
				}
				d := &reverseDispatcher{bridge: b, ctx: ctx}
				w, e := XM.NewServerWorker(XS.ContextWithIsReverseMux(ctx, true), d, reverseLink(c))
				err = e
				if err == nil {
					workers = append(workers, bridgeSession{w, d, c, cancel})
					context.AfterFunc(ctx, func() { w.Close(); c.Close() })
				}
			}
			if err != nil {
				cancel()
				if c != nil {
					c.Close()
				}
				if b.ctx.Err() == nil {
					b.logger.Warn("reverse bridge connection: ", err)
				}
			}
		}
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type reverseDispatcher struct {
	bridge   *reverseBridge
	ctx      context.Context
	draining atomic.Bool
}

func (d *reverseDispatcher) Type() any { return routing.DispatcherType() }
func (d *reverseDispatcher) Start() error      { return nil }
func (d *reverseDispatcher) Close() error      { return nil }
func (d *reverseDispatcher) Dispatch(ctx context.Context, dest XN.Destination) (*XT.Link, error) {
	if dest.Address.Family().IsDomain() && dest.Address.Domain() == "reverse" {
		upR, upW := XP.New(XP.WithSizeLimit(16384))
		downR, downW := XP.New(XP.WithSizeLimit(16384))
		go func() {
			defer upR.Interrupt()
			defer downW.Interrupt()
			stop := context.AfterFunc(d.ctx, func() { upR.Interrupt() })
			defer stop()
			for {
				mb, err := upR.ReadMultiBuffer()
				if err != nil {
					return
				}
				for _, b := range mb {
					var ctl XR.Control
					if proto.Unmarshal(b.Bytes(), &ctl) == nil {
						d.draining.Store(ctl.State == XR.Control_DRAIN)
					}
				}
				XB.ReleaseMulti(mb)
			}
		}()
		return &XT.Link{Reader: downR, Writer: upW}, nil
	}
	metadata := adapter.InboundContext{Inbound: d.bridge.tag, InboundType: "vless", Destination: sbDestination(dest)}
	if dest.Network == XN.Network_UDP {
		upR, upW := XP.New(XP.WithSizeLimit(65536))
		downR, downW := XP.New(XP.WithSizeLimit(65536))
		pc := newReversePacket(&XT.Link{Reader: upR, Writer: downW}, metadata.Destination)
		stop := context.AfterFunc(d.ctx, func() { pc.Close() })
		d.bridge.router.RoutePacketConnectionEx(d.ctx, pc, metadata, func(error) { stop(); pc.Close() })
		return &XT.Link{Reader: downR, Writer: upW}, nil
	}
	front, back := net.Pipe()
	stop := context.AfterFunc(d.ctx, func() { front.Close(); back.Close() })
	d.bridge.router.RouteConnectionEx(d.ctx, front, metadata, func(error) { stop(); front.Close() })
	return reverseLink(back), nil
}

func (d *reverseDispatcher) DispatchLink(context.Context, XN.Destination, *XT.Link) error {
	return fmt.Errorf("reverse dispatcher requires Dispatch")
}

// reversePacket preserves datagram boundaries and per-packet destinations across Xray mux.
type reverseDatagram struct {
	data []byte
	dest M.Socksaddr
	err  error
}
type reversePacket struct {
	link                      *XT.Link
	dest                      M.Socksaddr
	packets                   chan reverseDatagram
	mu                        sync.Mutex
	rd, wd                    time.Time
	readChanged, writeChanged chan struct{}
	writeMu                   sync.Mutex
	once                      sync.Once
	closed                    chan struct{}
}

func newReversePacket(l *XT.Link, d M.Socksaddr) *reversePacket {
	p := &reversePacket{link: l, dest: d, packets: make(chan reverseDatagram, 32), readChanged: make(chan struct{}), writeChanged: make(chan struct{}), closed: make(chan struct{})}
	go p.receive()
	return p
}

func (p *reversePacket) receive() {
	// There is one receiver goroutine; it alone closes packets, allowing
	// repeated ReadFrom calls to observe remote EOF instead of hanging.
	defer close(p.packets)
	for {
		mb, err := p.link.Reader.ReadMultiBuffer()
		for i, b := range mb {
			// Xray readers may return a nil buffer alongside EOF.
			// Do not dereference it while routing reverse UDP.
			if b == nil {
				continue
			}
			dest := p.dest
			if b.UDP != nil {
				dest = sbDestination(*b.UDP)
			}
			packet := reverseDatagram{data: append([]byte(nil), b.Bytes()...), dest: dest}
			b.Release()
			select {
			case p.packets <- packet:
			case <-p.closed:
				XB.ReleaseMulti(mb[i+1:])
				return
			}
		}
		if err != nil {
			select {
			case p.packets <- reverseDatagram{err: err}:
			case <-p.closed:
			}
			return
		}
	}
}

func deadlineTimer(t time.Time) (*time.Timer, <-chan time.Time) {
	if t.IsZero() {
		return nil, nil
	}
	timer := time.NewTimer(max(0, time.Until(t)))
	return timer, timer.C
}

func (p *reversePacket) read() (data []byte, dest M.Socksaddr, err error) {
	for {
		p.mu.Lock()
		deadline, changed := p.rd, p.readChanged
		p.mu.Unlock()
		timer, timed := deadlineTimer(deadline)
		select {
		case <-p.closed:
			err = net.ErrClosed
		case packet, ok := <-p.packets:
			if !ok {
				err = io.EOF
				break
			}
			data, dest, err = packet.data, packet.dest, packet.err
		case <-timed:
			err = os.ErrDeadlineExceeded
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		}
		if timer != nil {
			timer.Stop()
		}
		return
	}
}

func (p *reversePacket) ReadFrom(b []byte) (int, net.Addr, error) {
	data, d, err := p.read()
	if err != nil {
		return 0, nil, err
	}
	if len(data) > len(b) {
		return 0, nil, io.ErrShortBuffer
	}
	return copy(b, data), d, nil
}

func (p *reversePacket) ReadPacket(b *SB.Buffer) (M.Socksaddr, error) {
	data, d, err := p.read()
	if err != nil {
		return d, err
	}
	_, err = b.Write(data)
	return d, err
}

func (p *reversePacket) WriteTo(data []byte, a net.Addr) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	select {
	case <-p.closed:
		return 0, net.ErrClosed
	default:
	}
	if len(data) > XB.Size {
		return 0, fmt.Errorf("reverse mux UDP packet exceeds %d bytes", XB.Size)
	}
	p.mu.Lock()
	deadline := p.wd
	p.mu.Unlock()
	if !deadline.IsZero() && time.Until(deadline) <= 0 {
		return 0, os.ErrDeadlineExceeded
	}
	b := XB.NewWithSize(int32(len(data)))
	b.Write(data)
	dest := xrDestination(M.SocksaddrFromNet(a), true)
	b.UDP = &dest
	result := make(chan error, 1)
	go func() { result <- p.link.Writer.WriteMultiBuffer(XB.MultiBuffer{b}) }()
	for {
		p.mu.Lock()
		deadline, changed := p.wd, p.writeChanged
		p.mu.Unlock()
		timer, timed := deadlineTimer(deadline)
		var err error
		select {
		case err = <-result:
		case <-p.closed:
			err = net.ErrClosed
		case <-timed:
			p.Close()
			err = os.ErrDeadlineExceeded
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		}
		if timer != nil {
			timer.Stop()
		}
		if err != nil {
			return 0, err
		}
		return len(data), nil
	}
}

func (p *reversePacket) WritePacket(b *SB.Buffer, d M.Socksaddr) error {
	defer b.Release()
	_, err := p.WriteTo(b.Bytes(), d)
	return err
}

func (p *reversePacket) Close() error {
	p.once.Do(func() { close(p.closed); XC.Interrupt(p.link.Reader); XC.Interrupt(p.link.Writer) })
	return nil
}

func (p *reversePacket) LocalAddr() net.Addr { return &net.UDPAddr{} }
func (p *reversePacket) SetDeadline(t time.Time) error {
	p.SetReadDeadline(t)
	return p.SetWriteDeadline(t)
}

func (p *reversePacket) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	p.rd = t
	close(p.readChanged)
	p.readChanged = make(chan struct{})
	p.mu.Unlock()
	return nil
}

func (p *reversePacket) SetWriteDeadline(t time.Time) error {
	p.mu.Lock()
	p.wd = t
	close(p.writeChanged)
	p.writeChanged = make(chan struct{})
	p.mu.Unlock()
	return nil
}

type reverseBoundPacket struct {
	net.PacketConn
	dest M.Socksaddr
}

func newReverseBoundPacket(c net.PacketConn, d M.Socksaddr) *reverseBoundPacket {
	return &reverseBoundPacket{c, d}
}

func (p *reverseBoundPacket) Read(b []byte) (int, error)  { n, _, err := p.ReadFrom(b); return n, err }
func (p *reverseBoundPacket) Write(b []byte) (int, error) { return p.WriteTo(b, p.dest) }
func (p *reverseBoundPacket) RemoteAddr() net.Addr        { return p.dest }
