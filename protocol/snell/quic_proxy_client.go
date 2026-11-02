package snell

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func encodeQUICProxyPacket(psk, userKey []byte, destination M.Socksaddr, inner []byte) ([]byte, error) {
	host := destination.AddrString()
	if len(host) == 0 || len(host) > 255 || len(userKey) > 255 || destination.Port == 0 || !isQUICInitial(inner) {
		return nil, errors.New("snell QUIC: invalid initial, user key or destination")
	}
	plain := make([]byte, 0, 3+len(userKey)+1+len(host)+2+len(inner))
	plain = append(plain, 1, 1, byte(len(userKey)))
	plain = append(plain, userKey...)
	plain = append(plain, byte(len(host)))
	plain = append(plain, host...)
	plain = binary.BigEndian.AppendUint16(plain, destination.Port)
	plain = append(plain, inner...)
	if 16+23+len(plain)+16 > 65507 {
		return nil, errors.New("snell QUIC: payload too large")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := quicProxyAEAD(psk, salt)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, 7)
	hdr[0] = 4
	binary.BigEndian.PutUint16(hdr[5:], uint16(len(plain)))
	nonce := make([]byte, 12)
	out := append([]byte{}, salt...)
	out = append(out, aead.Seal(nil, nonce, hdr, nil)...)
	binary.LittleEndian.PutUint64(nonce, 1)
	return append(out, aead.Seal(nil, nonce, plain, nil)...), nil
}

// quicProxyLazyClient chooses UDP/UDP only for QUIC Initial datagrams.
// Other UDP retains the authenticated UDP-over-TCP path.
type quicProxyLazyClient struct {
	ctx           context.Context
	cancel        context.CancelFunc
	outbound      *Outbound
	destination   M.Socksaddr
	mu            sync.Mutex
	ready         chan struct{}
	conn          net.PacketConn
	startupErr    error
	closed        atomic.Bool
	readDeadline  quicProxyDeadline
	writeDeadline quicProxyDeadline
}

func newQUICProxyLazyClient(ctx context.Context, h *Outbound, dest M.Socksaddr) *quicProxyLazyClient {
	ctx, cancel := context.WithCancel(ctx)
	return &quicProxyLazyClient{ctx: ctx, cancel: cancel, outbound: h, destination: dest, ready: make(chan struct{})}
}

// Called with mu held. Close cancels the dial before taking mu.
func (c *quicProxyLazyClient) connect(initial []byte, addr net.Addr) error {
	if c.conn != nil || c.startupErr != nil {
		return c.startupErr
	}
	dest := c.destination
	if addr != nil {
		dest = M.SocksaddrFromNet(addr)
	}
	ctx := c.ctx
	if at, _ := c.writeDeadline.snapshot(); !at.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, at)
		defer cancel()
	}
	if dest.IsValid() && isQUICInitial(initial) {
		conn, err := c.outbound.dialer.DialContext(ctx, N.NetworkUDP, c.outbound.serverAddr)
		if err != nil {
			c.startupErr = err
			close(c.ready)
			return err
		}
		c.conn = &quicProxyDatagramConn{Conn: conn, destination: dest, psk: c.outbound.quicPSK, userKey: c.outbound.quicUserKey}
	} else {
		conn, err := c.outbound.dialer.DialContext(ctx, N.NetworkTCP, c.outbound.serverAddr)
		if err != nil {
			c.startupErr = err
			close(c.ready)
			return err
		}
		packet, err := c.outbound.client.DialPacketConn(conn)
		if err != nil {
			conn.Close()
			c.startupErr = err
			close(c.ready)
			return err
		}
		c.conn = packet
	}
	at, _ := c.readDeadline.snapshot()
	_ = c.conn.SetReadDeadline(at)
	at, _ = c.writeDeadline.snapshot()
	_ = c.conn.SetWriteDeadline(at)
	close(c.ready)
	return nil
}

func (c *quicProxyLazyClient) WriteTo(b []byte, addr net.Addr) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if c.writeDeadline.expired() {
		return 0, os.ErrDeadlineExceeded
	}
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	if err := c.connect(b, addr); err != nil {
		c.mu.Unlock()
		return 0, err
	}
	conn := c.conn
	c.mu.Unlock()
	return conn.WriteTo(b, addr)
}

func (c *quicProxyLazyClient) ReadFrom(b []byte) (int, net.Addr, error) {
	if err := c.readDeadline.wait(c.ctx, c.ready); err != nil {
		if c.closed.Load() {
			return 0, nil, net.ErrClosed
		}
		return 0, nil, err
	}
	c.mu.Lock()
	conn, err := c.conn, c.startupErr
	c.mu.Unlock()
	if err != nil {
		return 0, nil, err
	}
	if conn == nil {
		return 0, nil, net.ErrClosed
	}
	return conn.ReadFrom(b)
}

func (c *quicProxyLazyClient) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	c.cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil && c.startupErr == nil {
		close(c.ready)
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *quicProxyLazyClient) LocalAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.LocalAddr()
	}
	return &net.UDPAddr{}
}

func (c *quicProxyLazyClient) SetDeadline(t time.Time) error {
	c.readDeadline.set(t)
	c.writeDeadline.set(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.SetDeadline(t)
	}
	return nil
}

func (c *quicProxyLazyClient) SetReadDeadline(t time.Time) error {
	c.readDeadline.set(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.SetReadDeadline(t)
	}
	return nil
}

func (c *quicProxyLazyClient) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.set(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.SetWriteDeadline(t)
	}
	return nil
}

type quicProxyDatagramConn struct {
	net.Conn
	destination  M.Socksaddr
	psk, userKey []byte
	mu           sync.Mutex
	responded    atomic.Bool
}

func (c *quicProxyDatagramConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if addr != nil && M.SocksaddrFromNet(addr) != c.destination {
		return 0, errors.New("snell QUIC: cannot change destination within a flow")
	}
	// A lost first datagram must not strand the peer without its authenticated
	// mapping. Every retransmitted Initial is enveloped until a reply arrives.
	if !c.responded.Load() && isQUICInitial(p) {
		wrapped, err := encodeQUICProxyPacket(c.psk, c.userKey, c.destination, p)
		if err != nil {
			return 0, err
		}
		n, err := c.Conn.Write(wrapped)
		if err != nil {
			return 0, err
		}
		if n != len(wrapped) {
			return 0, errors.New("snell QUIC: short datagram write")
		}
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *quicProxyDatagramConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.responded.Store(true)
	}
	return n, c.destination, err
}
