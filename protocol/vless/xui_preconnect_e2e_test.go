package vless

import (
	"bytes"
	"context"
	stdtls "crypto/tls"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-vmess/vless"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Exercise actual TLS, VLESS framing, both Vision writers and the transport pool.
func TestXUIPanelVisionPreconnectRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keyPEM, certPEM, err := tls.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := stdtls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := stdtls.Listen("tcp", "127.0.0.1:0", &stdtls.Config{Certificates: []stdtls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	const id = "11111111-1111-4111-8111-111111111111"
	handler := adapter.NewUpstreamContextHandler(func(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
		defer conn.Close()
		io.Copy(conn, conn)
	}, func(context.Context, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {})
	service := vless.NewService[int](logger.NOP(), handler)
	service.UpdateUsers([]int{0}, []string{id}, []string{"xtls-rprx-vision"})
	var accepts atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				service.NewConnection(ctx, conn, M.Socksaddr{}, nil)
			}()
		}
	}()
	client, err := vless.NewClient(id, "xtls-rprx-vision", logger.NOP())
	if err != nil {
		t.Fatal(err)
	}
	config, err := tls.NewClient(ctx, logger.NOP(), "localhost", option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	physical, err := dialer.New(ctx, option.DialerOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	outbound := &Outbound{logger: logger.NOP(), dialer: physical, client: client, serverAddr: M.ParseSocksaddrHostPort("127.0.0.1", uint16(addr.Port)), tlsConfig: config, vision: true}
	outbound.preconnect = newPreconnectPool(ctx, 2, (*vlessDialer)(outbound).dialServer)
	defer outbound.Close()
	for i := 0; i < 3; i++ {
		conn, err := (*vlessDialer)(outbound).DialContext(ctx, "tcp", M.ParseSocksaddrHostPort("example.invalid", 80))
		if err != nil {
			t.Fatal(err)
		}
		payload := bytes.Repeat([]byte("vision payload "), 500)
		if _, err = conn.Write(payload); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		got := make([]byte, len(payload))
		_, err = io.ReadFull(conn, got)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("Vision data corrupted")
		}
	}
	if accepts.Load() < 3 {
		t.Fatal("preconnect reused a consumed connection")
	}
}
