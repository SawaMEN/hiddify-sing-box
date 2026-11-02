//go:build with_kcp

package v2raykcp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/hiddify/finalmask"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/transport/internet/kcp"
	"io"
	"net"
	"sync"
)

type LegacyClient struct {
	dialer   N.Dialer
	address  M.Socksaddr
	config   *kcp.Config
	masks    *finalmask.Masks
	tls      tls.Config
	mu       sync.Mutex
	closed   bool
	sessions map[*kcp.Connection]struct{}
}

func newLegacyClient(ctx context.Context, dialer N.Dialer, address M.Socksaddr, o option.V2RayKCPOptions, tlsConfig tls.Config) (*LegacyClient, error) {
	config, masks, err := buildOptions(o)
	if err != nil {
		return nil, err
	}
	return &LegacyClient{dialer: dialer, address: address, config: config, masks: masks, tls: tlsConfig, sessions: make(map[*kcp.Connection]struct{})}, nil
}
func (c *LegacyClient) DialContext(ctx context.Context) (net.Conn, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	raw, err := c.dialer.DialContext(ctx, N.NetworkUDP, c.address)
	if err != nil {
		return nil, err
	}
	if c.masks != nil {
		raw, err = c.masks.WrapConn(raw, N.NetworkUDP, c.address)
		if err != nil {
			return nil, err
		}
	}
	var conversation [2]byte
	if _, err = io.ReadFull(rand.Reader, conversation[:]); err != nil {
		raw.Close()
		return nil, err
	}
	session := kcp.NewConnection(kcp.ConnMetadata{LocalAddr: raw.LocalAddr(), RemoteAddr: raw.RemoteAddr(), Conversation: binary.BigEndian.Uint16(conversation[:])}, raw, raw, c.config)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		session.Terminate()
		return nil, net.ErrClosed
	}
	c.sessions[session] = struct{}{}
	c.mu.Unlock()
	go func() {
		defer func() { session.Terminate(); c.mu.Lock(); delete(c.sessions, session); c.mu.Unlock() }()
		var reader kcp.KCPPacketReader
		packet := make([]byte, 65536)
		for {
			n, err := raw.Read(packet)
			if err != nil {
				return
			}
			segments := reader.Read(packet[:n])
			if len(segments) > 0 {
				session.Input(segments)
			}
		}
	}()
	var conn net.Conn = session
	if c.tls != nil {
		conn, err = tls.ClientHandshake(ctx, session, c.tls)
		if err != nil {
			session.Terminate()
			return nil, err
		}
	}
	return conn, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	list := make([]*kcp.Connection, 0, len(c.sessions))
	for s := range c.sessions {
		list = append(list, s)
	}
	c.mu.Unlock()
	for _, s := range list {
		s.Terminate()
	}
	return nil
}
