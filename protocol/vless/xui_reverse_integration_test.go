package vless

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/experimental/v2rayapi"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	V "github.com/sagernet/sing-vmess/vless"
	B "github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
    XM "github.com/xtls/xray-core/common/mux"
    XS "github.com/xtls/xray-core/common/session"
)

// Native end-to-end parity for the retired 3x-ui reverse_runtime_test.go.in.
// Exercise the real authenticated VLESS wire and Xray mux rather than merely
// constructing a reverse portal with no bridge.

const reverseUUID = "11111111-1111-4111-8111-111111111111"

type reverseTestRouter struct {
	adapter.Router
	metadata chan adapter.InboundContext
}

func (r *reverseTestRouter) RouteConnectionEx(_ context.Context, c net.Conn, m adapter.InboundContext, close N.CloseHandlerFunc) {
	r.metadata <- m
	go func() { _, err := io.Copy(c, c); c.Close(); close(err) }()
}
func (r *reverseTestRouter) RoutePacketConnectionEx(_ context.Context, c N.PacketConn, m adapter.InboundContext, close N.CloseHandlerFunc) {
	r.metadata <- m
	go func() {
		defer c.Close()
		defer close(nil)
		for {
			b := B.NewSize(65535)
			dest, err := c.ReadPacket(b)
			if err != nil {
				b.Release()
				return
			}
			if err = c.WritePacket(b, dest); err != nil {
				return
			}
		}
	}()
}

type reverseTestManager struct {
	adapter.OutboundManager
	portal adapter.Outbound
}

func (m *reverseTestManager) Outbound(tag string) (adapter.Outbound, bool) {
	return m.portal, tag == m.portal.Tag()
}

type reverseTestAPI struct {
	adapter.V2RayServer
	stats *v2rayapi.StatsService
}

func (a *reverseTestAPI) StatsService() adapter.ConnectionTracker { return a.stats }

func TestXUIPanelReverseTCPUDPAndStats(t *testing.T) {
	ctx, cancel := context.WithCancel(service.ContextWithDefaultRegistry(context.Background()))
	defer cancel()
	logger := log.NewNOPFactory().Logger()
	ob, err := NewReversePortal(ctx, nil, logger, "portal", option.VLESSReversePortalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	portal := ob.(*reversePortal)
	defer portal.Close()
	service.MustRegister[adapter.OutboundManager](ctx, &reverseTestManager{portal: portal})
	stats := v2rayapi.NewStatsService(option.V2RayStatsServiceOptions{Enabled: true, Users: []string{"reverse-user"}, Inbounds: []string{"server"}})
	service.MustRegister[adapter.V2RayServer](ctx, &reverseTestAPI{stats: stats})
	in := &Inbound{Adapter: inbound.NewAdapter("vless", "server"), ctx: ctx, users: []option.VLESSUser{{Name: "reverse-user", UUID: reverseUUID, Reverse: &option.VLESSReverseOptions{Tag: "portal"}}}}
	server := V.NewService[int](logger, nil)
	server.UpdateUsers([]int{0}, []string{reverseUUID}, []string{""})
	server.SetReverseHandler(map[int]bool{0: true}, in.newReverseConnection)
	front, back := net.Pipe()
	defer front.Close()
	defer back.Close()
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.NewConnection(ctx, back, M.Socksaddr{}, nil) }()
	client, err := V.NewClient(reverseUUID, "", logger)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.DialEarlyReverseConnWithOptions(front, front, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write(nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverse auth timed out")
	}
	router := &reverseTestRouter{metadata: make(chan adapter.InboundContext, 32)}
	bridge := &reverseBridge{ctx: ctx, router: router, tag: "bridge"}
	dispatcher := &reverseDispatcher{bridge: bridge, ctx: ctx}
	worker, err := XM.NewServerWorker(XS.ContextWithIsReverseMux(ctx, true), dispatcher, reverseLink(conn))
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	for i := 0; i < 4; i++ {
		c, err := portal.DialContext(ctx, "tcp", M.ParseSocksaddr("example.org:443"))
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(3 * time.Second))
		payload := strings.Repeat("reverse TCP data ", 600)
		go func() { c.Write([]byte(payload)) }()
		got := make([]byte, len(payload))
		if _, err = io.ReadFull(c, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != payload {
			t.Fatal("TCP payload changed")
		}
		c.Close()
		m := <-router.metadata
		if m.Inbound != "bridge" || m.Destination.Fqdn != "example.org" {
			t.Fatalf("route metadata: %+v", m)
		}
	}
	pc, err := portal.ListenPacket(ctx, M.ParseSocksaddr("example.org:53"))
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	pc.SetDeadline(time.Now().Add(3 * time.Second))
	for _, payload := range [][]byte{nil, []byte("reverse UDP data"), bytes.Repeat([]byte{7}, 8192)} {
		for _, dest := range []M.Socksaddr{M.ParseSocksaddr("example.org:53"), M.ParseSocksaddr("127.0.0.2:5353")} {
			if _, err = pc.WriteTo(payload, dest); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 8192)
			n, addr, err := pc.ReadFrom(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got[:n], payload) || addr.String() != dest.String() {
				t.Fatalf("UDP payload/address: len %d / %d %s", n, len(payload), addr)
			}
		}
	}
	if _, err = pc.WriteTo(make([]byte, 8193), M.ParseSocksaddr("example.org:53")); err == nil {
		t.Fatal("oversized mux datagram accepted")
	}

	m := <-router.metadata
	if m.Inbound != "bridge" {
		t.Fatal("UDP reverse routing tag missing")
	}
	for _, direction := range []string{"uplink", "downlink"} {
		s, err := stats.GetStats(ctx, &v2rayapi.GetStatsRequest{Name: "user>>>reverse-user>>>traffic>>>" + direction})
		if err != nil || s.Stat.Value <= 0 {
			t.Fatalf("reverse user stats: %v %v", s, err)
		}
	}
	portal.Close()
	c, err := portal.DialContext(ctx, "tcp", M.ParseSocksaddr("example.org:80"))
	if err == nil {
		c.Close()
		t.Fatal("closed portal allowed new traffic")
	}
}

func TestXUIPanelReverseAuthorization(t *testing.T) {
	logger := log.NewNOPFactory().Logger()
	for _, tc := range []struct {
		name, id         string
		reverse, allowed bool
		flow             string
	}{
		{"ordinary user reverse", reverseUUID, true, false, ""},
		{"unknown UUID reverse", "22222222-2222-4222-8222-222222222222", true, true, ""},
		{"reverse user ordinary", reverseUUID, false, true, ""},
		{"flow mismatch", reverseUUID, true, true, "xtls-rprx-vision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := V.NewService[int](logger, nil)
			s.UpdateUsers([]int{0}, []string{reverseUUID}, []string{tc.flow})
			s.SetReverseHandler(map[int]bool{0: tc.allowed}, func(context.Context, int, net.Conn, M.Socksaddr, N.CloseHandlerFunc) error {
				t.Error("unauthorized reverse dispatched")
				return nil
			})
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			a.SetDeadline(time.Now().Add(time.Second))
			result := make(chan error, 1)
			go func() { result <- s.NewConnection(context.Background(), b, M.Socksaddr{}, nil) }()
			c, _ := V.NewClient(tc.id, "", logger)
			var conn net.Conn
			if tc.reverse {
				conn, _ = c.DialEarlyReverseConnWithOptions(a, a, false)
			} else {
				conn, _ = c.DialEarlyConn(a, M.ParseSocksaddr("example.org:80"))
			}
			conn.Write(nil)
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("unauthorized request accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("auth hung")
			}
		})
	}
}
func TestXUIPanelReverseMissingPortal(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(context.Background())
	wrong := &reversePortal{Adapter: outbound.NewAdapter("vless-reverse", "different", nil, nil)}
	service.MustRegister[adapter.OutboundManager](ctx, &reverseTestManager{portal: wrong})
	in := &Inbound{ctx: ctx, users: []option.VLESSUser{{Reverse: &option.VLESSReverseOptions{Tag: "missing"}}}}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := in.newReverseConnection(ctx, 0, a, M.Socksaddr{}, nil); err == nil {
		t.Fatal("unknown portal authorized")
	}
}

