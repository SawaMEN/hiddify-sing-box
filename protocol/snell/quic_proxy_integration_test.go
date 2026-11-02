package snell

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/htest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type quicTestRouter struct {
	adapter.Router
	users    chan string
	up, down atomic.Int64
}

func (r *quicTestRouter) RoutePacketConnectionEx(ctx context.Context, flow N.PacketConn, m adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.users <- m.User
	go func() {
		conn, err := net.Dial("udp", m.Destination.String())
		if err != nil {
			flow.Close()
			onClose(err)
			return
		}
		defer conn.Close()
		defer flow.Close()
		reverse := make(chan struct{})
		go func() {
			defer close(reverse)
			defer flow.Close()
			data := make([]byte, 65535)
			for {
				n, err := conn.Read(data)
				if err != nil {
					return
				}
				r.down.Add(int64(n))
				head := N.CalculateFrontHeadroom(flow)
				packet := buf.NewSize(head + n + N.CalculateRearHeadroom(flow))
				packet.Resize(head, 0)
				_, _ = packet.Write(data[:n])
				if err = flow.WritePacket(packet, m.Destination); err != nil {
					return
				}
			}
		}()
		for {
			packet := buf.NewPacket()
			_, err = flow.ReadPacket(packet)
			if err != nil {
				packet.Release()
				break
			}
			r.up.Add(int64(packet.Len()))
			_, err = conn.Write(packet.Bytes())
			packet.Release()
			if err != nil {
				break
			}
		}
		conn.Close()
		<-reverse
		if onClose != nil {
			onClose(err)
		}
	}()
}

func freeV5Port(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := M.SocksaddrFromNet(l.Addr()).Port
	l.Close()
	return p
}

func startV5Inbound(t *testing.T) (M.Socksaddr, *quicTestRouter, *Inbound) {
	t.Helper()
	router := &quicTestRouter{users: make(chan string, 64)}
	port := freeV5Port(t)
	// JSON exercises the public version-specific options decoder as well.
	var opts option.SnellInboundOptions
	raw := fmt.Sprintf(`{"version":5,"psk":"snell-v5-integration-key","quic_proxy":true,"listen":"127.0.0.1","listen_port":%d,"users":[{"name":"alice","userkey":"alice-key"},{"name":"bob","userkey":"bob-key"}]}`, port)
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		t.Fatal(err)
	}
	in, err := NewInbound(context.Background(), router, log.NewNOPFactory().Logger(), "v5", opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = htest.Start(in, adapter.StartStateStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = htest.Close(in) })
	return M.ParseSocksaddrHostPort("127.0.0.1", port), router, in.(*Inbound)
}

func v5Outbound(t *testing.T, server M.Socksaddr, key string) *Outbound {
	t.Helper()
	var opts option.SnellOutboundOptions
	raw := fmt.Sprintf(`{"version":5,"server":"%s","server_port":%d,"psk":"snell-v5-integration-key","userkey":%q}`, server.AddrString(), server.Port, key)
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		t.Fatal(err)
	}
	out, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), "v5", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.(*Outbound).Close() })
	return out.(*Outbound)
}

func serveV5HTTP3(t *testing.T) M.Socksaddr {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: priv}))
	if err != nil {
		t.Fatal(err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}}, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write(data)
	})}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(pc) }()
	t.Cleanup(func() { server.Close(); pc.Close(); <-done })
	return M.SocksaddrFromNet(pc.LocalAddr())
}

func exchangeV5HTTP3(t *testing.T, out *Outbound, dest M.Socksaddr, version quic.Version, bound bool) {
	t.Helper()
	exchangeV5HTTP3For(t, out, dest, version, bound, 0)
}

func exchangeV5HTTP3For(t *testing.T, out *Outbound, dest M.Socksaddr, version quic.Version, bound bool, duration time.Duration) {
	t.Helper()
	var pc net.PacketConn
	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, QUICConfig: &quic.Config{Versions: []quic.Version{version}, HandshakeIdleTimeout: 5 * time.Second, MaxIdleTimeout: 10 * time.Second}, Dial: func(ctx context.Context, _ string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
		var err error
		if bound {
			var c net.Conn
			c, err = out.DialContext(ctx, "udp", dest)
			if err == nil {
				pc = c.(net.PacketConn)
			}
		} else {
			pc, err = out.ListenPacket(ctx, dest)
		}
		if err != nil {
			return nil, err
		}
		return quic.DialEarly(ctx, pc, dest, tlsCfg, cfg)
	}}
	defer tr.Close()
	defer func() {
		if pc != nil {
			pc.Close()
		}
	}()
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	end := time.Now().Add(duration)
	for i := 0; i < 10 || time.Now().Before(end); i++ {
		if duration > 0 {
			time.Sleep(50 * time.Millisecond)
		}
		payload := bytes.Repeat([]byte{byte(i)}, 65536+i)
		resp, err := client.Post("https://localhost/echo", "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.ProtoMajor != 3 || resp.StatusCode != 200 || !bytes.Equal(got, payload) {
			t.Fatal("HTTP/3 response corruption")
		}
	}
}

func TestSnellV5HTTP3(t *testing.T) {
	dest := serveV5HTTP3(t)
	server, router, _ := startV5Inbound(t)
	for _, v := range []quic.Version{quic.Version1, quic.Version2} {
		for _, bound := range []bool{false, true} {
			t.Run(fmt.Sprintf("version=%v/bound=%v", v, bound), func(t *testing.T) { exchangeV5HTTP3(t, v5Outbound(t, server, "alice-key"), dest, v, bound) })
		}
	}
	if router.up.Load() == 0 || router.down.Load() == 0 {
		t.Fatal("traffic accounting is empty")
	}
	for range 4 {
		select {
		case user := <-router.users:
			if user != "alice" {
				t.Fatal("wrong accounting user", user)
			}
		case <-time.After(time.Second):
			t.Fatal("missing routed QUIC flow")
		}
	}
}

func TestSnellV5OfficialServer(t *testing.T) {
	binary := os.Getenv("SNELL_V5_SERVER")
	if binary == "" {
		t.Skip("set SNELL_V5_SERVER to official snell-server v5.0.1")
	}
	port := freeV5Port(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "snell.conf")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("[snell-server]\nlisten = 127.0.0.1:%d\npsk = snell-v5-integration-key\n", port)), 0o600); err != nil {
		t.Fatal(err)
	}
	logfile, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logfile.Close()
	cmd := exec.Command(binary, "-c", cfg)
	cmd.Stdout = logfile
	cmd.Stderr = logfile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address := M.ParseSocksaddrHostPort("127.0.0.1", port)
	ready := false
	for range 100 {
		conn, err := net.DialTimeout("tcp", address.String(), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(logfile.Name())
		t.Fatalf("official server did not start: %s", data)
	}
	dest := serveV5HTTP3(t)
	for _, v := range []quic.Version{quic.Version1, quic.Version2} {
		t.Run(fmt.Sprint(v), func(t *testing.T) { exchangeV5HTTP3(t, v5Outbound(t, address, ""), dest, v, false) })
	}
}

func TestSnellV5LazyReadDeadline(t *testing.T) {
	c := newQUICProxyLazyClient(context.Background(), &Outbound{}, M.Socksaddr{})
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, _, err := c.ReadFrom(make([]byte, 10)); done <- err }()
	c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read deadline ignored before first write")
	}
}

func TestSnellV5QueueOverflowKeepsSession(t *testing.T) {
	f := &quicProxySession{input: make(chan []byte, 1), done: make(chan struct{})}
	f.enqueue([]byte{1})
	f.enqueue([]byte{2})
	select {
	case <-f.done:
		t.Fatal("UDP loss closed session")
	default:
	}
}

func TestSnellV5SessionDeadline(t *testing.T) {
	f := &quicProxySession{input: make(chan []byte, 1), done: make(chan struct{})}
	done := make(chan error, 1)
	go func() { b := buf.NewPacket(); defer b.Release(); _, err := f.ReadPacket(b); done <- err }()
	f.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server deadline ignored")
	}
}

// Drop both an outgoing authenticated Initial and the first server reply.
// QUIC must recover without sending Snell framing to the destination server.
type v5LossDialer struct {
	v6TestDialer
	remaining atomic.Int32
}

func (d *v5LossDialer) DialContext(ctx context.Context, network string, dest M.Socksaddr) (net.Conn, error) {
	c, err := d.v6TestDialer.DialContext(ctx, network, dest)
	if err != nil {
		return nil, err
	}
	if network == "udp" {
		return &v5LossConn{Conn: c, remaining: &d.remaining}, nil
	}
	return c, nil
}

type v5LossConn struct {
	net.Conn
	remaining *atomic.Int32
}

func (c *v5LossConn) Write(p []byte) (int, error) {
	if c.remaining.Add(-1) >= 0 {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

type v5LossWriter struct {
	N.PacketWriter
	remaining atomic.Int32
}

func (w *v5LossWriter) WritePacket(b *buf.Buffer, dest M.Socksaddr) error {
	if w.remaining.Add(-1) >= 0 {
		b.Release()
		return nil
	}
	return w.PacketWriter.WritePacket(b, dest)
}

func TestSnellV5HTTP3PacketLoss(t *testing.T) {
	dest := serveV5HTTP3(t)
	server, router, in := startV5Inbound(t)
	dialer := &v5LossDialer{}
	dialer.remaining.Store(1)
	out := v5Outbound(t, server, "bob-key")
	out.dialer = dialer
	in.quicProxy.mu.Lock()
	writer := &v5LossWriter{PacketWriter: in.quicProxy.writer}
	writer.remaining.Store(1)
	in.quicProxy.writer = writer
	in.quicProxy.mu.Unlock()
	exchangeV5HTTP3(t, out, dest, quic.Version1, false)
	select {
	case user := <-router.users:
		if user != "bob" {
			t.Fatal(user)
		}
	case <-time.After(time.Second):
		t.Fatal("missing routed flow")
	}
	select {
	case <-router.users:
		t.Fatal("retransmission created a second flow")
	default:
	}
}

func TestSnellV5HTTP3Soak(t *testing.T) {
	text := os.Getenv("SNELL_V5_SOAK_TEST")
	if text == "" {
		t.Skip("set SNELL_V5_SOAK_TEST=3m for sustained HTTP/3 test")
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 {
		t.Fatal("invalid soak duration")
	}
	dest := serveV5HTTP3(t)
	server, _, _ := startV5Inbound(t)
	for _, v := range []quic.Version{quic.Version1, quic.Version2} {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			t.Parallel()
			exchangeV5HTTP3For(t, v5Outbound(t, server, "alice-key"), dest, v, false, duration)
		})
	}
}

func TestSnellV5UDPOverTCPFallback(t *testing.T) {
	server, router, _ := startV5Inbound(t)
	echo, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		data := make([]byte, 2048)
		for {
			n, addr, err := echo.ReadFrom(data)
			if err != nil {
				return
			}
			_, _ = echo.WriteTo(data[:n], addr)
		}
	}()
	defer func() { echo.Close(); <-done }()
	dest := M.SocksaddrFromNet(echo.LocalAddr())
	packet, err := v5Outbound(t, server, "bob-key").ListenPacket(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	packet.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("ordinary non-QUIC UDP")
	for range 100 {
		if _, err = packet.WriteTo(payload, dest); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 2048)
		n, addr, err := packet.ReadFrom(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[:n], payload) || addr.String() != dest.String() {
			t.Fatal("UDP fallback corruption")
		}
	}
	select {
	case user := <-router.users:
		if user != "bob" {
			t.Fatal(user)
		}
	case <-time.After(time.Second):
		t.Fatal("missing UDP fallback user")
	}
}

type v5BlockedDialer struct {
	v6TestDialer
	started chan struct{}
}

func (d *v5BlockedDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	close(d.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSnellV5CloseCancelsStartup(t *testing.T) {
	d := &v5BlockedDialer{started: make(chan struct{})}
	out := &Outbound{dialer: d}
	c := newQUICProxyLazyClient(context.Background(), out, M.ParseSocksaddr("127.0.0.1:443"))
	written := make(chan error, 1)
	go func() { _, err := c.WriteTo(testQUICInitial(), M.ParseSocksaddr("127.0.0.1:443")); written <- err }()
	<-d.started
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind dial")
	}
	if err := <-written; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type v5CaptureRouter struct {
	adapter.Router
	flows chan N.PacketConn
	users chan string
}

func (r *v5CaptureRouter) RoutePacketConnectionEx(_ context.Context, c N.PacketConn, m adapter.InboundContext, _ N.CloseHandlerFunc) {
	r.users <- m.User
	r.flows <- c
}

type v5DiscardWriter struct{}

func (v5DiscardWriter) WritePacket(b *buf.Buffer, _ M.Socksaddr) error { b.Release(); return nil }
func TestSnellV5RetransmissionAuthAndExpiry(t *testing.T) {
	router := &v5CaptureRouter{flows: make(chan N.PacketConn, 4), users: make(chan string, 4)}
	s := newQUICProxyServer(context.Background(), router, log.NewNOPFactory().Logger(), "v5", []byte("test-v5-psk"), []option.SnellUser{{Name: "alice", UserKey: "alice-key"}, {Name: "bob", UserKey: "bob-key"}})
	s.writer = v5DiscardWriter{}
	defer s.close()
	source, dest := M.ParseSocksaddr("127.0.0.1:10001"), M.ParseSocksaddr("127.0.0.1:443")
	initial := testQUICInitial()
	send := func(key string, payload []byte) {
		t.Helper()
		packet, err := encodeQUICProxyPacket(s.psk, []byte(key), dest, payload)
		if err != nil {
			t.Fatal(err)
		}
		s.handle(buf.As(packet), source)
	}
	send("alice-key", initial)
	var flow N.PacketConn
	select {
	case flow = <-router.flows:
	case <-time.After(time.Second):
		t.Fatal("no flow")
	}
	if user := <-router.users; user != "alice" {
		t.Fatal(user)
	}
	read := func() {
		t.Helper()
		b := buf.NewPacket()
		defer b.Release()
		_, err := flow.ReadPacket(b)
		if err != nil || !bytes.Equal(b.Bytes(), initial) {
			t.Fatal("envelope forwarded as raw QUIC", err)
		}
	}
	read()
	send("alice-key", initial)
	read()
	send("bob-key", initial)
	flow.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
	b := buf.NewPacket()
	_, err := flow.ReadPacket(b)
	b.Release()
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("another user injected into flow", err)
	}
	select {
	case <-router.flows:
		t.Fatal("duplicate flow")
	default:
	}
	f := flow.(*quicProxySession)
	f.lastSeen.Store(time.Now().Add(-10 * time.Minute).UnixNano())
	s.reapIdle(time.Now().Add(-quicProxyIdle).UnixNano())
	select {
	case <-f.done:
	default:
		t.Fatal("expired session retained")
	}
	s.mu.Lock()
	remaining := len(s.flows)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("expired session still mapped")
	}
	// An authenticated Initial from the same tuple can establish a fresh flow.
	send("alice-key", initial)
	select {
	case next := <-router.flows:
		if next == flow {
			t.Fatal("closed flow reused")
		}
		next.Close()
	case <-time.After(time.Second):
		t.Fatal("reconnect after expiry failed")
	}
}
