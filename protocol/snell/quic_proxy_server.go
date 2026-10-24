package snell

// Snell v5 QUIC Proxy: the first datagram authenticates and encrypts the
// destination and QUIC Initial. Later datagrams remain raw, preserving QUIC
// congestion control. Per-user accounting goes through the native UDP router.
import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"golang.org/x/crypto/argon2"
)

const (
	quicProxyIdle = 5 * time.Minute
	quicProxyMaxSessions = 2048
	quicProxyMaxDatagram = 65535
)

func quicProxyAEAD(psk, salt []byte) (cipher.AEAD, error) {
	if len(salt) != 16 || len(psk) == 0 {
		return nil, errors.New("snell QUIC: invalid key or salt")
	}
	key := argon2.IDKey(psk, salt, 3, 8192, 1, 32)
	block, err := aes.NewCipher(key[:16])
	if err != nil { return nil, err }
	return cipher.NewGCM(block)
}

// decodeQUICProxyPacket implements the v5 envelope using bounds-checked
// lengths and authenticated AES-GCM. It never dials unauthenticated targets.
func decodeQUICProxyPacket(psk, packet []byte) (M.Socksaddr, string, []byte, error) {
	var empty M.Socksaddr
	// A QUIC Initial is padded to at least 1200 bytes; reject smaller
	// unauthenticated datagrams before the expensive Argon2id derivation.
	if len(packet) < 1200 || len(packet) > quicProxyMaxDatagram {
		return empty, "", nil, errors.New("snell QUIC: truncated envelope")
	}
	aead, err := quicProxyAEAD(psk, packet[:16])
	if err != nil { return empty, "", nil, err }
	header, err := aead.Open(nil, make([]byte, 12), packet[16:39], nil)
	if err != nil { return empty, "", nil, err }
	if len(header) != 7 || header[0] != 4 {
		return empty, "", nil, errors.New("snell QUIC: invalid record header")
	}
	padding := int(binary.BigEndian.Uint16(header[3:5]))
	payloadLen := int(binary.BigEndian.Uint16(header[5:7]))
	start := 39 + padding
	if payloadLen < 7 || start > len(packet) || payloadLen + aead.Overhead() > len(packet)-start {
		return empty, "", nil, errors.New("snell QUIC: invalid payload length")
	}
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce, 1)
	plain, err := aead.Open(nil, nonce, packet[start:start+payloadLen+aead.Overhead()], nil)
	if err != nil { return empty, "", nil, err }
	if len(plain) < 7 || plain[0] != 1 || plain[1] != 1 {
		return empty, "", nil, errors.New("snell QUIC: invalid request")
	}
	keyEnd := 3 + int(plain[2])
	if keyEnd+1 > len(plain) { return empty, "", nil, errors.New("snell QUIC: invalid user key") }
	hostEnd := keyEnd+1+int(plain[keyEnd])
	if hostEnd+2 > len(plain) { return empty, "", nil, errors.New("snell QUIC: invalid destination") }
	host := string(plain[keyEnd+1:hostEnd])
	port := binary.BigEndian.Uint16(plain[hostEnd:hostEnd+2])
	inner := plain[hostEnd+2:]
	if host == "" || port == 0 || !isQUICInitial(inner) {
		return empty, "", nil, errors.New("snell QUIC: expected QUIC Initial")
	}
	return M.ParseSocksaddrHostPort(host, port), string(plain[3:keyEnd]), inner, nil
}

func isQUICInitial(packet []byte) bool {
	if len(packet) < 5 {
		return false
	}
	version := binary.BigEndian.Uint32(packet[1:5])
	if version == 0x6b3343cf { // QUIC v2
		return packet[0]&0xf0 == 0xd0
	}
	return version != 0 && packet[0]&0xf0 == 0xc0
}

type quicProxyServer struct {
	ctx context.Context
	router adapter.ConnectionRouterEx
	logger logger.ContextLogger
	tag string
	psk []byte
	users map[string]string
	mu sync.Mutex
	flows map[string]*quicProxySession
	writer N.PacketWriter
	stop chan struct{}
	closed bool
}

func newQUICProxyServer(ctx context.Context, router adapter.ConnectionRouterEx, logger logger.ContextLogger, tag string, psk []byte, users []option.SnellUser) *quicProxyServer {
	s := &quicProxyServer{ctx:ctx, router:router, logger:logger, tag:tag, psk:psk, users:make(map[string]string), flows:make(map[string]*quicProxySession), stop:make(chan struct{})}
	for _, user := range users { s.users[user.UserKey] = user.Name }
	return s
}

func (s *quicProxyServer) start(writer N.PacketWriter) {
	s.mu.Lock()
	s.writer = writer
	s.mu.Unlock()
	go s.reap()
}

func (s *quicProxyServer) handle(packet *buf.Buffer, source M.Socksaddr) {
	data := append([]byte(nil), packet.Bytes()...)
	packet.Release()
	key := source.String()
	s.mu.Lock()
	if s.closed { s.mu.Unlock(); return }
	if flow := s.flows[key]; flow != nil {
		s.mu.Unlock()
		flow.enqueue(data)
		return
	}
	s.mu.Unlock()
	dest, userKey, initial, err := decodeQUICProxyPacket(s.psk, data)
	if err != nil { return }
	name, allowed := s.matchQUICUser(userKey)
	if !allowed { return }
	flow := &quicProxySession{server:s, key:key, source:source, destination:dest, input:make(chan []byte, 64), done:make(chan struct{})}
	flow.lastSeen.Store(time.Now().UnixNano())
	s.mu.Lock()
	if s.closed || len(s.flows) >= quicProxyMaxSessions { s.mu.Unlock(); return }
	if existing := s.flows[key]; existing != nil {
		s.mu.Unlock()
		existing.enqueue(initial)
		return
	}
	s.flows[key] = flow
	s.mu.Unlock()
	flow.enqueue(initial)
	metadata := adapter.InboundContext{Inbound:s.tag, InboundType:"snell", Source:source, Destination:dest, User:name}
	ctx := log.ContextWithNewID(s.ctx)
	go s.router.RoutePacketConnectionEx(ctx, flow, metadata, func(error) { _ = flow.Close() })
}

// A PSK-only Surge v5 client can be attributed to the sole enabled user.
// With multiple users the userkey is mandatory to avoid bypassing quotas.
func (s *quicProxyServer) matchQUICUser(userKey string) (string, bool) {
	if len(s.users) == 0 {
		return "", true
	}
	name, exists := s.users[userKey]
	if exists {
		return name, true
	}
	if userKey == "" && len(s.users) == 1 {
		for _, only := range s.users {
			return only, true
		}
	}
	return "", false
}

func (s *quicProxyServer) reap() {
	ticker := time.NewTicker(30*time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop: return
		case <-s.ctx.Done(): return
		case <-ticker.C:
		}
		cutoff := time.Now().Add(-quicProxyIdle).UnixNano()
		var expired []*quicProxySession
		s.mu.Lock()
		for _, flow := range s.flows {
			if flow.lastSeen.Load() < cutoff { expired = append(expired, flow) }
		}
		s.mu.Unlock()
		for _, flow := range expired { _ = flow.Close() }
	}
}

func (s *quicProxyServer) close() {
	s.mu.Lock()
	if s.closed { s.mu.Unlock(); return }
	s.closed = true
	close(s.stop)
	flows := make([]*quicProxySession,0,len(s.flows))
	for _, f := range s.flows { flows=append(flows,f) }
	s.mu.Unlock()
	for _, f := range flows { _ = f.Close() }
}

type quicProxySession struct {
	server *quicProxyServer
	key string
	source M.Socksaddr
	destination M.Socksaddr
	input chan []byte
	done chan struct{}
	once sync.Once
	lastSeen atomic.Int64
}

func (f *quicProxySession) enqueue(data []byte) {
	select {
	case <-f.done: return
	case f.input <- data:
		f.lastSeen.Store(time.Now().UnixNano())
	default:
		_ = f.Close()
	}
}

func (f *quicProxySession) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	select {
	case <-f.done: return M.Socksaddr{}, net.ErrClosed
	case packet := <-f.input:
		_, err := buffer.Write(packet)
		return f.destination,err
	}
}
func (f *quicProxySession) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	select {
	case <-f.done: buffer.Release(); return net.ErrClosed
	default:
	}
	f.lastSeen.Store(time.Now().UnixNano())
	return f.server.writer.WritePacket(buffer, f.source)
}
func (f *quicProxySession) Close() error {
	f.once.Do(func() {
		close(f.done)
		f.server.mu.Lock()
		if f.server.flows[f.key] == f { delete(f.server.flows,f.key) }
		f.server.mu.Unlock()
	})
	return nil
}
func (f *quicProxySession) LocalAddr() net.Addr { return f.source.UDPAddr() }
func (f *quicProxySession) SetDeadline(time.Time) error { return nil }
func (f *quicProxySession) SetReadDeadline(time.Time) error { return nil }
func (f *quicProxySession) SetWriteDeadline(time.Time) error { return nil }
