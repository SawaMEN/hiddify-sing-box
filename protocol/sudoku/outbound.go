package sudoku

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	cfg "github.com/sagernet/sing-box/transport/compat/sudoku/config"
	crypt "github.com/sagernet/sing-box/transport/compat/sudoku/pkg/crypto"
	codec "github.com/sagernet/sing-box/transport/compat/sudoku/pkg/obfs/sudoku"
	"github.com/sagernet/sing-box/transport/compat/sudoku/tunnel"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"sync"
	"time"
)

func RegisterOutbound(r *outbound.Registry) { outbound.Register(r, "sudoku", NewOutbound) }

type Outbound struct {
	outbound.Adapter
	base        tunnel.BaseDialer
	mux         *tunnel.MuxDialer
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	connections map[*ownedConn]struct{}
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, o option.SudokuOutboundOptions) (adapter.Outbound, error) {
	if len(o.CustomTables) > 16 {
		return nil, fmt.Errorf("sudoku: at most 16 appearance tables are supported")
	}
	if o.Server == "" || o.ServerPort == 0 || o.Key == "" {
		return nil, fmt.Errorf("sudoku: server, port and key are required")
	}
	c := &cfg.Config{Mode: "client", Transport: "tcp", LocalPort: 1080, ServerAddress: o.ServerOptions.Build().String(), Key: o.Key, ASCII: o.ASCII, AEAD: o.AEAD, CustomTables: o.CustomTables, EnablePureDownlink: o.EnablePureDownlink, PaddingMin: o.PaddingMin, PaddingMax: o.PaddingMax, HTTPMask: cfg.HTTPMaskConfig{Disable: o.HTTPMask.Disable, Mode: o.HTTPMask.Mode, TLS: o.HTTPMask.TLS, Host: o.HTTPMask.Host, PathRoot: o.HTTPMask.PathRoot, Multiplex: o.HTTPMask.Multiplex}}
	if c.AEAD == "" {
		c.AEAD = "chacha20-poly1305"
	}
	if err := c.Finalize(); err != nil {
		return nil, fmt.Errorf("sudoku: invalid configuration")
	}
	var private []byte
	if pub, err := crypt.RecoverPublicKey(c.Key); err == nil {
		private, err = hex.DecodeString(c.Key)
		if err != nil {
			return nil, fmt.Errorf("sudoku: invalid key")
		}
		c.Key = crypt.EncodePoint(pub)
	}
	tables, err := codec.NewTableSet(c.Key, c.ASCII, c.CustomTables)
	if err != nil {
		return nil, fmt.Errorf("sudoku: invalid appearance tables")
	}
	d, err := dialer.New(ctx, o.DialerOptions, o.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	h := &Outbound{Adapter: outbound.NewAdapterWithDialerOptions("sudoku", tag, []string{N.NetworkTCP, N.NetworkUDP}, o.DialerOptions), ctx: life, cancel: cancel}
	h.base = tunnel.BaseDialer{Config: c, Tables: tables.Tables, PrivateKey: private, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, M.ParseSocksaddr(a))
	}}
	if c.SessionMuxEnabled() {
		h.mux = &tunnel.MuxDialer{BaseDialer: h.base}
	}
	return h, nil
}
func (h *Outbound) open(ctx context.Context, udp bool, dst M.Socksaddr) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	if !udp && h.mux != nil {
		c, err := h.mux.DialContext(ctx, dst.String())
		if err != nil {
			return nil, err
		}
		return h.own(c)
	}
	c, err := h.base.DialBaseContext(ctx)
	if err != nil {
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { c.Close() })
	defer stopClose()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if udp {
		err = tunnel.WriteKIPMessage(c, tunnel.KIPTypeStartUoT, nil)
	} else {
		err = tunnel.WriteOpenTCP(c, dst.String())
	}
	if err != nil || ctx.Err() != nil {
		c.Close()
		if err == nil {
			err = ctx.Err()
		}
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return h.own(c)
}
func (h *Outbound) DialContext(ctx context.Context, n string, dst M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(n) {
	case N.NetworkTCP:
		return h.open(ctx, false, dst)
	case N.NetworkUDP:
		p, err := h.ListenPacket(ctx, dst)
		if err != nil {
			return nil, err
		}
		return &packetStream{PacketConn: p, remote: dst}, nil
	default:
		return nil, N.ErrUnknownNetwork
	}
}
func (h *Outbound) ListenPacket(ctx context.Context, dst M.Socksaddr) (net.PacketConn, error) {
	c, err := h.open(ctx, true, dst)
	if err != nil {
		return nil, err
	}
	return &packetConn{Conn: c}, nil
}
func (h *Outbound) Close() error {
	h.cancel()
	h.mu.Lock()
	h.closed = true
	connections := make([]*ownedConn, 0, len(h.connections))
	for c := range h.connections {
		connections = append(connections, c)
	}
	h.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
	if h.mux != nil {
		return h.mux.Close()
	}
	return nil
}

type packetConn struct {
	net.Conn
	readMu, writeMu sync.Mutex
}

func (p *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()
	a, data, err := tunnel.ReadUoTDatagram(p.Conn)
	if err != nil {
		return 0, nil, err
	}
	if len(data) > len(b) {
		return 0, nil, io.ErrShortBuffer
	}
	return copy(b, data), M.ParseSocksaddr(a), nil
}
func (p *packetConn) WriteTo(b []byte, a net.Addr) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if err := tunnel.WriteUoTDatagram(p.Conn, a.String(), b); err != nil {
		return 0, err
	}
	return len(b), nil
}

type packetStream struct {
	net.PacketConn
	remote net.Addr
}

func (p *packetStream) Read(b []byte) (int, error)  { n, _, e := p.ReadFrom(b); return n, e }
func (p *packetStream) Write(b []byte) (int, error) { return p.WriteTo(b, p.remote) }
func (p *packetStream) RemoteAddr() net.Addr        { return p.remote }

// Registration and shutdown share one lock so a completed handshake cannot
// escape an outbound Close racing with it.
func (h *Outbound) own(c net.Conn) (net.Conn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.ctx.Err() != nil {
		c.Close()
		return nil, net.ErrClosed
	}
	if h.connections == nil {
		h.connections = make(map[*ownedConn]struct{})
	}
	owned := &ownedConn{Conn: c, owner: h}
	h.connections[owned] = struct{}{}
	return owned, nil
}

type ownedConn struct {
	net.Conn
	owner *Outbound
	once  sync.Once
	err   error
}

func (c *ownedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
	})
	return c.err
}
