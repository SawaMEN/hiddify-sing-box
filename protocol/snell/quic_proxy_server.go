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
	"os"
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
	quicProxyIdle        = 5 * time.Minute
	quicProxyMaxSessions = 2048
	quicProxyMaxDatagram = 65535
)

func quicProxyAEAD(psk, salt []byte) (cipher.AEAD, error) {
	if len(salt) != 16 || len(psk) == 0 {
		return nil, errors.New("snell QUIC: invalid key or salt")
	}
	key := argon2.IDKey(psk, salt, 3, 8, 1, 32)
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		return nil, err
	}
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
	if err != nil {
		return empty, "", nil, err
	}
	header, err := aead.Open(nil, make([]byte, 12), packet[16:39], nil)
	if err != nil {
		return empty, "", nil, err
	}
	if len(header) != 7 || header[0] != 4 {
		return empty, "", nil, errors.New("snell QUIC: invalid record header")
	}
	padding := int(binary.BigEndian.Uint16(header[3:5]))
	payloadLen := int(binary.BigEndian.Uint16(header[5:7]))
	start := 39 + padding
	if payloadLen < 7 || start > len(packet) || payloadLen+aead.Overhead() > len(packet)-start {
		return empty, "", nil, errors.New("snell QUIC: invalid payload length")
	}
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce, 1)
	plain, err := aead.Open(nil, nonce, packet[start:start+payloadLen+aead.Overhead()], nil)
	if err != nil {
		return empty, "", nil, err
	}
	if len(plain) < 7 || plain[0] != 1 || plain[1] != 1 {
		return empty, "", nil, errors.New("snell QUIC: invalid request")
	}
	keyEnd := 3 + int(plain[2])
	if keyEnd+1 > len(plain) {
		return empty, "", nil, errors.New("snell QUIC: invalid user key")
	}
	hostEnd := keyEnd + 1 + int(plain[keyEnd])
	if hostEnd+2 > len(plain) {
		return empty, "", nil, errors.New("snell QUIC: invalid destination")
	}
	host := string(plain[keyEnd+1 : hostEnd])
	port := binary.BigEndian.Uint16(plain[hostEnd : hostEnd+2])
	inner := plain[hostEnd+2:]
	if host == "" || port == 0 || !isQUICInitial(inner) {
		return empty, "", nil, errors.New("snell QUIC: expected QUIC Initial")
	}
	return M.ParseSocksaddrHostPort(host, port), string(plain[3:keyEnd]), inner, nil
}

func isQUICInitial(packet []byte) bool {
	if len(packet) < 1200 {
		return false
	}
	version := binary.BigEndian.Uint32(packet[1:5])
	if version == 0x6b3343cf { // QUIC v2
		return packet[0]&0xf0 == 0xd0
	}
	return version == 1 && packet[0]&0xf0 == 0xc0
}

type quicProxyServer struct {
	ctx    context.Context
	router adapter.ConnectionRouterEx
	logger logger.ContextLogger
	tag    string
	psk    []byte
	users  map[string]string
	mu     sync.Mutex
	flows  map[string]*quicProxySession
	writer N.PacketWriter
	stop   chan struct{}
	closed bool
}

func newQUICProxyServer(ctx context.Context, router adapter.ConnectionRouterEx, logger logger.ContextLogger, tag string, psk []byte, users []option.SnellUser) *quicProxyServer {
	s := &quicProxyServer{ctx: ctx, router: router, logger: logger, tag: tag, psk: psk, users: make(map[string]string), flows: make(map[string]*quicProxySession), stop: make(chan struct{})}
	for _, user := range users {
		s.users[user.UserKey] = user.Name
	}
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
	if s.closed || s.writer == nil {
		s.mu.Unlock()
		return
	}
	if flow := s.flows[key]; flow != nil {
		s.mu.Unlock()
		s.forwardExisting(flow, data)
		return
	}
	s.mu.Unlock()
	dest, userKey, initial, err := decodeQUICProxyPacket(s.psk, data)
	if err != nil {
		return
	}
	name, allowed := s.matchQUICUser(userKey)
	if !allowed {
		return
	}
	flow := &quicProxySession{server: s, key: key, source: source, destination: dest, user: name, input: make(chan []byte, 64), done: make(chan struct{})}
	flow.lastSeen.Store(time.Now().UnixNano())
	s.mu.Lock()
	if s.closed || len(s.flows) >= quicProxyMaxSessions {
		s.mu.Unlock()
		return
	}
	if existing := s.flows[key]; existing != nil {
		s.mu.Unlock()
		s.forwardExisting(existing, data)
		return
	}
	s.flows[key] = flow
	s.mu.Unlock()
	flow.enqueue(initial)
	metadata := adapter.InboundContext{Inbound: s.tag, InboundType: "snell", Source: source, Destination: dest, User: name}
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
	defer s.close()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		s.reapIdle(time.Now().Add(-quicProxyIdle).UnixNano())
	}
}

func (s *quicProxyServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stop)
	flows := make([]*quicProxySession, 0, len(s.flows))
	for _, f := range s.flows {
		flows = append(flows, f)
	}
	s.mu.Unlock()
	for _, f := range flows {
		_ = f.Close()
	}
}

type quicProxySession struct {
	server        *quicProxyServer
	key           string
	source        M.Socksaddr
	destination   M.Socksaddr
	user          string
	input         chan []byte
	done          chan struct{}
	once          sync.Once
	lastSeen      atomic.Int64
	readDeadline  quicProxyDeadline
	writeDeadline quicProxyDeadline
}

func (f *quicProxySession) enqueue(data []byte) {
	f.lastSeen.Store(time.Now().UnixNano())
	select {
	case <-f.done:
		return
	case f.input <- data:
		f.lastSeen.Store(time.Now().UnixNano())
	default:
		// Drop under congestion; QUIC handles loss and retransmission.
	}
}

func (f *quicProxySession) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	for {
		at, changed := f.readDeadline.snapshot()
		if !at.IsZero() && !time.Now().Before(at) {
			return M.Socksaddr{}, os.ErrDeadlineExceeded
		}
		var timer *time.Timer
		var timeout <-chan time.Time
		if !at.IsZero() {
			timer = time.NewTimer(time.Until(at))
			timeout = timer.C
		}
		select {
		case <-f.done:
			if timer != nil {
				timer.Stop()
			}
			return M.Socksaddr{}, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-timeout:
			return M.Socksaddr{}, os.ErrDeadlineExceeded
		case packet := <-f.input:
			if timer != nil {
				timer.Stop()
			}
			if len(packet) > buffer.FreeLen() {
				return M.Socksaddr{}, os.ErrInvalid
			}
			_, err := buffer.Write(packet)
			return f.destination, err
		}
	}
}

func (f *quicProxySession) WritePacket(buffer *buf.Buffer, _ M.Socksaddr) error {
	if f.writeDeadline.expired() {
		buffer.Release()
		return os.ErrDeadlineExceeded
	}
	select {
	case <-f.done:
		buffer.Release()
		return net.ErrClosed
	default:
	}
	f.lastSeen.Store(time.Now().UnixNano())
	f.server.mu.Lock()
	writer := f.server.writer
	f.server.mu.Unlock()
	if writer == nil {
		buffer.Release()
		return net.ErrClosed
	}
	return writer.WritePacket(buffer, f.source)
}

func (f *quicProxySession) Close() error {
	f.once.Do(func() {
		close(f.done)
		f.server.mu.Lock()
		if f.server.flows[f.key] == f {
			delete(f.server.flows, f.key)
		}
		f.server.mu.Unlock()
	})
	return nil
}
func (f *quicProxySession) LocalAddr() net.Addr { return f.source.UDPAddr() }
func (f *quicProxySession) SetDeadline(t time.Time) error {
	f.readDeadline.set(t)
	f.writeDeadline.set(t)
	return nil
}
func (f *quicProxySession) SetReadDeadline(t time.Time) error  { f.readDeadline.set(t); return nil }
func (f *quicProxySession) SetWriteDeadline(t time.Time) error { f.writeDeadline.set(t); return nil }

// Repeated authenticated Initials may arrive before the first response.
// Never forward the encrypted Snell envelope as if it were QUIC payload.
func (s *quicProxyServer) forwardExisting(flow *quicProxySession, data []byte) {
	if dest, key, inner, err := decodeQUICProxyPacket(s.psk, data); err == nil {
		name, ok := s.matchQUICUser(key)
		if !ok || dest != flow.destination || name != flow.user {
			return
		}
		flow.enqueue(inner)
		return
	}
	flow.enqueue(data)
}

func (s *quicProxyServer) reapIdle(cutoff int64) {
	var expired []*quicProxySession
	s.mu.Lock()
	for _, flow := range s.flows {
		if flow.lastSeen.Load() < cutoff {
			expired = append(expired, flow)
		}
	}
	s.mu.Unlock()
	for _, flow := range expired {
		if flow.lastSeen.Load() < cutoff {
			_ = flow.Close()
		}
	}
}
