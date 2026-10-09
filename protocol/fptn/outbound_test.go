package fptn

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/sagernet/sing-box/internal/paneltest"
	"github.com/sagernet/sing-box/option"
	wire "github.com/sagernet/sing-box/transport/fptn"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

// Independent server implementation of FPTN's packed C++ obfuscator header.
type serverWire struct {
	net.Conn
	handshake atomic.Bool
	pending   []byte
}

func (c *serverWire) Read(p []byte) (int, error) {
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	if !c.handshake.Load() {
		return c.Conn.Read(p)
	}
	h := make([]byte, 22)
	if _, e := io.ReadFull(c.Conn, h); e != nil {
		return 0, e
	}
	n := int(binary.BigEndian.Uint16(h[18:20]))
	pad := int(binary.BigEndian.Uint16(h[20:22]))
	if n+pad+17 != int(binary.BigEndian.Uint16(h[3:5])) {
		return 0, io.ErrUnexpectedEOF
	}
	b := make([]byte, n+pad)
	if _, e := io.ReadFull(c.Conn, b); e != nil {
		return 0, e
	}
	for i := range n {
		b[i] ^= h[17]
	}
	c.pending = b[:n]
	return c.Read(p)
}
func (c *serverWire) Write(p []byte) (int, error) {
	if !c.handshake.Load() {
		return c.Conn.Write(p)
	}
	h := make([]byte, 22+len(p))
	copy(h, []byte{23, 3, 3})
	binary.BigEndian.PutUint16(h[3:5], uint16(17+len(p)))
	binary.BigEndian.PutUint32(h[13:17], uint32(time.Now().Unix()))
	h[17] = 0x5a
	binary.BigEndian.PutUint16(h[18:20], uint16(len(p)))
	for i := range p {
		h[22+i] = p[i] ^ 0x5a
	}
	_, e := c.Conn.Write(h)
	return len(p), e
}

type serverListener struct {
	net.Listener
	config *tls.Config
}

func (l serverListener) Accept() (net.Conn, error) {
	raw, e := l.Listener.Accept()
	if e != nil {
		return nil, e
	}
	raw.SetDeadline(time.Now().Add(15 * time.Second))
	c := &serverWire{Conn: raw}
	c.handshake.Store(true)
	ssl := tls.Server(c, l.config)
	if e = ssl.Handshake(); e != nil {
		raw.Close()
		return nil, e
	}
	c.handshake.Store(false)
	raw.SetDeadline(time.Time{})
	return ssl, nil
}

type testDialer struct{}

func (testDialer) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, n, d.String())
}
func (testDialer) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	return net.ListenPacket("udp", "127.0.0.1:0")
}
func TestPinnedTLSProtobufTCPUDP(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	sum := md5.Sum(der)
	pin := hex.EncodeToString(sum[:])
	conf := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "user" || body["password"] != "password" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"access_token":"test-jwt"}`)
	})
	done := make(chan struct{})
	handler.HandleFunc("/api/v1/fptn", func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		if r.Header.Get("Authorization") != "Bearer test-jwt" || r.Header.Get("X-Serializer") == "yaff" {
			w.WriteHeader(401)
			return
		}
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		assignment := append([]byte{10, 9}, []byte("10.23.0.2")...)
		assignment = append(assignment, 18, 7)
		assignment = append(assignment, []byte("fd23::2")...)
		msg := append([]byte{8, 1, 16, 2, 42, byte(len(assignment))}, assignment...)
		if ws.WriteMessage(websocket.BinaryMessage, msg) != nil {
			return
		}
		peer := paneltest.NewPeer(t, []netip.Addr{netip.MustParseAddr("10.23.0.1"), netip.MustParseAddr("fd23::1")}, func(p []byte) error { return ws.WriteMessage(websocket.BinaryMessage, wire.EncodePacket(p)) })
		defer peer.Close()
		for {
			_, b, e := ws.ReadMessage()
			if e != nil {
				return
			}
			packets, e := wire.DecodePackets(b)
			if e != nil {
				return
			}
			for _, p := range packets {
				peer.Inject(p)
			}
		}
	})
	srv := &http.Server{Handler: handler}
	go srv.Serve(serverListener{listener, conf})
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := M.ParseSocksaddr(listener.Addr().String())
	h := &Outbound{ctx: ctx, cancel: cancel, opts: option.FPTNOutboundOptions{ServerOptions: option.ServerOptions{Server: addr.Addr.String(), ServerPort: addr.Port}, Username: "user", Password: "password", Fingerprint: pin, SNI: "www.google.com", Bypass: "obfuscation", MTU: 1500}, dialer: testDialer{}}
	defer h.Close()
	for _, ip := range []string{"10.23.0.1", "fd23::1"} {
		for _, n := range []string{"tcp", "udp"} {
			port := uint16(8080)
			if n == "udp" {
				port = 8081
			}
			c, e := h.DialContext(ctx, n, M.SocksaddrFrom(netip.MustParseAddr(ip), port))
			if e != nil {
				t.Fatal(e)
			}
			paneltest.Echo(t, c)
		}

	}

	packet, e := h.ListenPacket(ctx, M.SocksaddrFrom(netip.MustParseAddr("10.23.0.1"), 8081))
	if e != nil {
		t.Fatal(e)
	}
	packet.SetDeadline(time.Now().Add(10 * time.Second))
	data := []byte("unconnected UDP")
	if _, e = packet.WriteTo(data, M.SocksaddrFrom(netip.MustParseAddr("10.23.0.1"), 8081)); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1500)
	n, _, e := packet.ReadFrom(b)
	if e != nil || string(b[:n]) != string(data) {
		t.Fatalf("UDP packet round trip: %v", e)
	}
	packet.Close()
	badCreds := &Outbound{ctx: ctx, cancel: func() {}, opts: h.opts, dialer: testDialer{}}
	badCreds.opts.Password = "wrong"
	if _, e = badCreds.ensure(ctx); e == nil {
		t.Fatal("wrong credentials accepted")
	}
	badCreds.Close()
	badPin := &Outbound{ctx: ctx, cancel: func() {}, opts: h.opts, dialer: testDialer{}}
	badPin.opts.Fingerprint = "00000000000000000000000000000000"
	if _, e = badPin.ensure(ctx); e == nil {
		t.Fatal("wrong certificate pin accepted")
	}
	badPin.Close()
	h.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("FPTN session did not close")
	}
}
