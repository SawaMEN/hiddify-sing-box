//go:build with_kcp

package v2raykcp

import (
	"context"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/hiddify/finalmask"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/xtls/xray-core/transport/internet/kcp"
	"net"
	"os"
	"sync"
)

type legacySessionID struct {
	address      string
	conversation uint16
}
type LegacyServer struct {
	ctx      context.Context
	config   *kcp.Config
	masks    *finalmask.Masks
	tls      tls.ServerConfig
	handler  adapter.V2RayServerTransportHandler
	mu       sync.Mutex
	closed   bool
	packet   net.PacketConn
	sessions map[legacySessionID]*kcp.Connection
}

func newLegacyServer(ctx context.Context, logger logger.ContextLogger, o option.V2RayKCPOptions, tlsConfig tls.ServerConfig, handler adapter.V2RayServerTransportHandler) (*LegacyServer, error) {
	config, masks, err := buildOptions(o)
	if err != nil {
		return nil, err
	}
	return &LegacyServer{ctx: ctx, config: config, masks: masks, tls: tlsConfig, handler: handler, sessions: make(map[legacySessionID]*kcp.Connection)}, nil
}
func (s *LegacyServer) Network() []string        { return []string{N.NetworkUDP} }
func (s *LegacyServer) Serve(net.Listener) error { return os.ErrInvalid }
func (s *LegacyServer) ServePacket(packet net.PacketConn) error {
	var err error
	if s.masks != nil && s.masks.HasUDP() {
		raw := packet
		packet, err = s.masks.WrapPacketConnServer(raw)
		if err != nil {
			raw.Close()
			return err
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		packet.Close()
		return net.ErrClosed
	}
	s.packet = packet
	s.mu.Unlock()
	var reader kcp.KCPPacketReader
	data := make([]byte, 65536)
	for {
		n, addr, err := packet.ReadFrom(data)
		if err != nil {
			return err
		}
		segments := reader.Read(data[:n])
		if len(segments) == 0 {
			continue
		}
		id := legacySessionID{addr.String(), segments[0].Conversation()}
		s.mu.Lock()
		conn := s.sessions[id]
		if conn == nil {
			if s.closed {
				s.mu.Unlock()
				return net.ErrClosed
			}
			if segments[0].Command() == kcp.CommandTerminate || len(s.sessions) >= 4096 {
				s.mu.Unlock()
				continue
			}
			writer := &legacyPacketWriter{packet: packet, address: addr, server: s, id: id}
			conn = kcp.NewConnection(kcp.ConnMetadata{LocalAddr: packet.LocalAddr(), RemoteAddr: addr, Conversation: id.conversation}, writer, writer, s.config)
			s.sessions[id] = conn
			go s.handle(conn, addr)
		}
		s.mu.Unlock()
		conn.Input(segments)
	}
}
func (s *LegacyServer) handle(session *kcp.Connection, addr net.Addr) {
	var conn net.Conn = session
	var err error
	if s.tls != nil {
		conn, err = tls.ServerHandshake(s.ctx, session, s.tls)
		if err != nil {
			session.Terminate()
			return
		}
	}
	s.handler.NewConnectionEx(s.ctx, conn, M.SocksaddrFromNet(addr), M.Socksaddr{}, nil)
}
func (s *LegacyServer) Close() error {
	s.mu.Lock()
	s.closed = true
	packet := s.packet
	list := make([]*kcp.Connection, 0, len(s.sessions))
	for _, c := range s.sessions {
		list = append(list, c)
	}
	s.mu.Unlock()
	if packet != nil {
		packet.Close()
	}
	for _, c := range list {
		c.Terminate()
	}
	return nil
}

type legacyPacketWriter struct {
	packet  net.PacketConn
	address net.Addr
	server  *LegacyServer
	id      legacySessionID
}

func (w *legacyPacketWriter) Write(p []byte) (int, error) { return w.packet.WriteTo(p, w.address) }
func (w *legacyPacketWriter) Close() error {
	w.server.mu.Lock()
	delete(w.server.sessions, w.id)
	w.server.mu.Unlock()
	return nil
}
