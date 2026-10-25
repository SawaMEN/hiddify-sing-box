package snell

import (
	"bytes"
	"context"
	"fmt"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-snell/snellv6"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

const v6TestPSK = "snell-v6-integration-psk"

type v6EchoRouter struct {
	adapter.Router
	mu    sync.Mutex
	users []string
}

func (r *v6EchoRouter) record(user string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users = append(r.users, user)
}
func (r *v6EchoRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, m adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.record(m.User)
	go func() {
		_, err := io.Copy(conn, conn)
		conn.Close()
		if onClose != nil {
			onClose(err)
		}
	}()
}
func (r *v6EchoRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, m adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.record(m.User)
	go func() {
		defer conn.Close()
		var err error
		defer func() {
			if onClose != nil {
				onClose(err)
			}
		}()
		for {
			packet := buf.NewPacket()
			packet.Resize(N.CalculateFrontHeadroom(conn), 0)
			var dest M.Socksaddr
			dest, err = conn.ReadPacket(packet)
			if err != nil {
				packet.Release()
				return
			}
			err = conn.WritePacket(packet, dest)
			if err != nil {
				return
			}
		}
	}()
}

type v6TestDialer struct{}

func (v6TestDialer) DialContext(ctx context.Context, network string, dest M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, dest.String())
}
func (v6TestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return net.ListenPacket("udp", "127.0.0.1:0")
}
func startV6TestInbound(t *testing.T, mode string) (M.Socksaddr, *v6EchoRouter) {
	t.Helper()
	router := &v6EchoRouter{}
	inbound, err := NewInbound(context.Background(), router, log.NewNOPFactory().Logger(), "snell-test", option.SnellInboundOptions{Version: 6, AbstractSnellInboundOptions: option.AbstractSnellInboundOptions{PSK: v6TestPSK, Users: []option.SnellUser{{Name: "alice", UserKey: "alice-key"}, {Name: "bob", UserKey: "bob-key"}}}, V6Options: option.SnellV6Options{Mode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var active sync.Map
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			active.Store(conn, struct{}{})
			wg.Add(1)
			go func() {
				defer wg.Done()
				inbound.(*Inbound).NewConnection(context.Background(), conn, adapter.InboundContext{Source: M.SocksaddrFromNet(conn.RemoteAddr())}, func(error) { active.Delete(conn) })
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		active.Range(func(k, v any) bool { k.(net.Conn).Close(); return true })
		wg.Wait()
		inbound.Close()
	})
	return M.SocksaddrFromNet(listener.Addr()), router
}
func v6TestClient(t *testing.T, server M.Socksaddr, mode, key string, reuse bool) *snellv6.Client {
	t.Helper()
	parsed, _ := snellv6.ParseMode(mode)
	client, err := snellv6.NewClient(snellv6.ClientOptions{PSK: []byte(v6TestPSK), UserKey: []byte(key), Mode: parsed, Reuse: reuse, Dialer: v6TestDialer{}, Server: server})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}
func TestSnellV6TCPReconnect(t *testing.T) {
	for _, mode := range []string{"default", "unshaped"} {
		for _, reuse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reuse=%v", mode, reuse), func(t *testing.T) {
				server, router := startV6TestInbound(t, mode)
				client := v6TestClient(t, server, mode, "alice-key", reuse)
				for i := 0; i < 120; i++ {
					if i == 60 {
						client.Reset()
					}
					conn, err := client.DialContext(context.Background(), M.ParseSocksaddrHostPort("127.0.0.1", 443))
					if err != nil {
						t.Fatal(err)
					}
					conn.SetDeadline(time.Now().Add(5 * time.Second))
					payload := bytes.Repeat([]byte{byte(i)}, 32768+i)
					done := make(chan error, 1)
					go func() { _, err := conn.Write(payload); done <- err }()
					got := make([]byte, len(payload))
					if _, err = io.ReadFull(conn, got); err != nil {
						conn.Close()
						t.Fatalf("session %d: %v", i, err)
					}
					if err = <-done; err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						t.Fatal("TCP corruption")
					}
					if reuse {
						if err = N.CloseWrite(conn); err != nil {
							t.Fatal(err)
						}
						var tail [1]byte
						if _, err = conn.Read(tail[:]); err != io.EOF {
							t.Fatalf("logical EOF: %v", err)
						}
					}
					conn.Close()
				}
				router.mu.Lock()
				defer router.mu.Unlock()
				if len(router.users) != 120 {
					t.Fatalf("routed %d sessions", len(router.users))
				}
				for _, user := range router.users {
					if user != "alice" {
						t.Fatalf("wrong user %q", user)
					}
				}
			})
		}
	}
}
func TestSnellV6UDPAndAuthentication(t *testing.T) {
	for _, mode := range []string{"default", "unshaped"} {
		t.Run(mode, func(t *testing.T) {
			server, router := startV6TestInbound(t, mode)
			for _, key := range []string{"bob-key", "invalid-key", ""} {
				client := v6TestClient(t, server, mode, key, false)
				raw, err := net.Dial("tcp", server.String())
				if err != nil {
					t.Fatal(err)
				}
				raw.SetDeadline(time.Now().Add(5 * time.Second))
				packet, err := client.DialPacketConn(raw)
				if err != nil {
					raw.Close()
					t.Fatal(err)
				}
				dest := M.ParseSocksaddrHostPort("127.0.0.1", 53)
				for i := 0; i < 100; i++ {
					payload := bytes.Repeat([]byte{byte(i)}, 1400+i)
					_, err = packet.WriteTo(payload, dest)
					if err != nil {
						if key != "bob-key" {
							break
						}
						t.Fatal(err)
					}
					got := make([]byte, 2048)
					n, addr, err := packet.ReadFrom(got)
					if key != "bob-key" {
						if err == nil {
							t.Fatal("unauthenticated UDP accepted")
						}
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got[:n], payload) || addr.String() != dest.String() {
						t.Fatal("UDP corruption")
					}
				}
				packet.Close()
				client.Close()
			}
			router.mu.Lock()
			defer router.mu.Unlock()
			if len(router.users) != 1 || router.users[0] != "bob" {
				t.Fatalf("unexpected users: %v", router.users)
			}
		})
	}
}
func TestSnellV6RejectsInvalidTCPKeys(t *testing.T) {
	for _, mode := range []string{"default", "unshaped"} {
		t.Run(mode, func(t *testing.T) {
			server, router := startV6TestInbound(t, mode)
			parsed, _ := snellv6.ParseMode(mode)
			for _, tc := range []struct{ psk, key string }{{v6TestPSK, "invalid-key"}, {v6TestPSK, ""}, {"wrong-server-psk", "alice-key"}} {
				client, err := snellv6.NewClient(snellv6.ClientOptions{PSK: []byte(tc.psk), UserKey: []byte(tc.key), Mode: parsed, Dialer: v6TestDialer{}, Server: server})
				if err != nil {
					t.Fatal(err)
				}
				conn, err := client.DialContext(context.Background(), M.ParseSocksaddrHostPort("127.0.0.1", 443))
				if err != nil {
					client.Close()
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err = conn.Write([]byte("unauthenticated")); err == nil {
					var got [1]byte
					if _, err = conn.Read(got[:]); err == nil {
						t.Fatal("invalid credentials reached routing")
					}
				}
				conn.Close()
				client.Close()
			}
			router.mu.Lock()
			defer router.mu.Unlock()
			if len(router.users) != 0 {
				t.Fatal("unauthenticated sessions routed")
			}
		})
	}
}
func TestSnellV6OutboundTCPAndUDP(t *testing.T) {
	for _, mode := range []string{"default", "unshaped"} {
		t.Run(mode, func(t *testing.T) {
			server, _ := startV6TestInbound(t, mode)
			out, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), "snell-out", option.SnellOutboundOptions{Version: 6, AbstractSnellOutboundOptions: option.AbstractSnellOutboundOptions{ServerOptions: option.ServerOptions{Server: server.AddrString(), ServerPort: server.Port}, PSK: v6TestPSK, UserKey: "alice-key"}, V6Options: option.SnellV6Options{Mode: mode}})
			if err != nil {
				t.Fatal(err)
			}
			client := out.(*Outbound)
			defer client.Close()
			dest := M.ParseSocksaddrHostPort("127.0.0.1", 53)
			for _, network := range []string{"tcp", "udp"} {
				conn, err := client.DialContext(context.Background(), network, dest)
				if err != nil {
					t.Fatal(err)
				}
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				payload := []byte("native outbound " + network)
				if _, err = conn.Write(payload); err != nil {
					conn.Close()
					t.Fatal(err)
				}
				got := make([]byte, len(payload))
				if _, err = io.ReadFull(conn, got); err != nil {
					conn.Close()
					t.Fatal(err)
				}
				conn.Close()
				if !bytes.Equal(got, payload) {
					t.Fatal("payload corruption")
				}
			}
			packet, err := client.ListenPacket(context.Background(), dest)
			if err != nil {
				t.Fatal(err)
			}
			defer packet.Close()
			packet.SetDeadline(time.Now().Add(5 * time.Second))
			payload := []byte("native packet outbound")
			if _, err = packet.WriteTo(payload, dest); err != nil {
				t.Fatal(err)
			}
			got := make([]byte, 1024)
			n, _, err := packet.ReadFrom(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got[:n], payload) {
				t.Fatal("UDP corruption")
			}
		})
	}
}
func TestSnellV6IdleRecovery(t *testing.T) {
	durationText := os.Getenv("SNELL_V6_IDLE_TEST")
	if durationText == "" {
		t.Skip("set SNELL_V6_IDLE_TEST=130s for idle recovery test")
	}
	idle, err := time.ParseDuration(durationText)
	if err != nil || idle < 0 {
		t.Fatal("invalid idle duration")
	}
	for _, mode := range []string{"default", "unshaped"} {
		for _, reuse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reuse=%v", mode, reuse), func(t *testing.T) {
				t.Parallel()
				server, _ := startV6TestInbound(t, mode)
				client := v6TestClient(t, server, mode, "alice-key", reuse)
				conn, err := client.DialContext(context.Background(), M.ParseSocksaddrHostPort("127.0.0.1", 443))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				raw, err := net.Dial("tcp", server.String())
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
				udp, err := client.DialPacketConn(raw)
				if err != nil {
					t.Fatal(err)
				}
				defer udp.Close()
				exchange := func() {
					t.Helper()
					conn.SetDeadline(time.Now().Add(5 * time.Second))
					udp.SetDeadline(time.Now().Add(5 * time.Second))
					payload := []byte("snell-v6-idle-recovery")
					if _, err = conn.Write(payload); err != nil {
						t.Fatal(err)
					}
					got := make([]byte, len(payload))
					if _, err = io.ReadFull(conn, got); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, payload) {
						t.Fatal("TCP corruption")
					}
					dest := M.ParseSocksaddrHostPort("127.0.0.1", 53)
					if _, err = udp.WriteTo(payload, dest); err != nil {
						t.Fatal(err)
					}
					got = make([]byte, 1024)
					n, _, err := udp.ReadFrom(got)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got[:n], payload) {
						t.Fatal("UDP corruption")
					}
				}
				exchange()
				conn.SetDeadline(time.Time{})
				udp.SetDeadline(time.Time{})
				timer := time.NewTimer(idle)
				defer timer.Stop()
				<-timer.C
				for i := 0; i < 20; i++ {
					exchange()
				}
			})
		}
	}
}
