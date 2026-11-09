package vless

import (
    "context"
    "crypto/tls"
    "io"
    "net"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/sagernet/sing-box/adapter"
    "github.com/sagernet/sing-box/experimental/v2rayapi"
    "github.com/sagernet/sing-box/log"
    "github.com/sagernet/sing-box/option"
    V "github.com/sagernet/sing-vmess/vless"
    M "github.com/sagernet/sing/common/metadata"
    "github.com/sagernet/sing/service"
)

// Full connection lifecycle for the native bridge: post-start, TCP echo,
// routed bytes, reconnection after transport loss and close without a hang.

func TestXUIPanelReverseNativeBridgeReconnectAndShutdown(t *testing.T) {
	for _, flow := range []string{"", "xtls-rprx-vision"} {
		t.Run("TLS-"+flow, func(t *testing.T) { testReverseNativeBridge(t, flow) })
	}
}
func testReverseNativeBridge(t *testing.T, flow string) {
	ctx, cancel := context.WithCancel(service.ContextWithDefaultRegistry(context.Background()))
	defer cancel()
	logger := log.NewNOPFactory().Logger()
	ob, _ := NewReversePortal(ctx, nil, logger, "portal", option.VLESSReversePortalOptions{})
	portal := ob.(*reversePortal)
	defer portal.Close()
	service.MustRegister[adapter.OutboundManager](ctx, &reverseTestManager{portal: portal})
	stats := v2rayapi.NewStatsService(option.V2RayStatsServiceOptions{Enabled: true, Outbounds: []string{"remote"}})
	service.MustRegister[adapter.V2RayServer](ctx, &reverseTestAPI{stats: stats})
	in := &Inbound{Adapter: inbound.NewAdapter("vless", "server"), ctx: ctx, users: []option.VLESSUser{{UUID: reverseUUID, Flow: flow, Reverse: &option.VLESSReverseOptions{Tag: "portal"}}}}
	s := V.NewService[int](logger, nil)
	s.UpdateUsers([]int{0}, []string{reverseUUID}, []string{flow})
	s.SetReverseHandler(map[int]bool{0: true}, in.newReverseConnection)
	certServer := httptest.NewTLSServer(nil)
	tlsConfig := certServer.TLS.Clone()
	certServer.Close()
	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := tls.NewListener(rawListener, tlsConfig)
	defer listener.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- c
			go func() {
				if err := s.NewConnection(ctx, c, M.SocksaddrFromNet(c.RemoteAddr()), nil); err != nil {
					c.Close()
				}
			}()
		}
	}()
	router := &reverseTestRouter{metadata: make(chan adapter.InboundContext, 32)}
	bridge, err := NewOutbound(ctx, router, logger, "remote", option.VLESSOutboundOptions{ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: uint16(listener.Addr().(*net.TCPAddr).Port)}, UUID: reverseUUID, Flow: flow, OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}}, Reverse: &option.VLESSReverseOptions{Tag: "bridge"}})
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.(io.Closer).Close()
	if err = bridge.(adapter.Lifecycle).Start(adapter.StartStatePostStart, nil); err != nil {
		t.Fatal(err)
	}
	var first net.Conn
	select {
	case first = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not connect")
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		if _, err := portal.PickAvailable(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bridge not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, err := portal.DialContext(ctx, "tcp", M.ParseSocksaddr("test.example:8080"))
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	go c.Write([]byte("native reverse"))
	got := make([]byte, 14)
	if _, err = io.ReadFull(c, got); err != nil || string(got) != "native reverse" {
		t.Fatalf("native bridge: %q %v", got, err)
	}
	c.Close()
	for _, direction := range []string{"uplink", "downlink"} {
		result, err := stats.GetStats(ctx, &v2rayapi.GetStatsRequest{Name: "outbound>>>remote>>>traffic>>>" + direction})
		if err != nil || result.Stat.Value <= 0 {
			t.Fatalf("reverse outbound stats: %v %v", result, err)
		}
	}
	first.Close()
	select {
	case second := <-accepted:
		defer second.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not reconnect")
	}
	done := make(chan struct{})
	go func() { bridge.(io.Closer).Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge shutdown hung")
	}
}

