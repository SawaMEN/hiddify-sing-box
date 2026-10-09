package sudoku

import (
	"context"
	"github.com/sagernet/sing-box/internal/paneltest"
	"github.com/sagernet/sing-box/option"
	cfg "github.com/sagernet/sing-box/transport/compat/sudoku/config"
	codec "github.com/sagernet/sing-box/transport/compat/sudoku/pkg/obfs/sudoku"
	"github.com/sagernet/sing-box/transport/compat/sudoku/tunnel"
	M "github.com/sagernet/sing/common/metadata"
	"io"
	"net"
	"testing"
	"time"
)

func TestEncryptedHandshakeTCPUDP(t *testing.T) {
	for _, pure := range []bool{true, false} {
		t.Run(map[bool]string{true: "pure", false: "packed"}[pure], func(t *testing.T) {
			c := &cfg.Config{Mode: "client", Transport: "tcp", LocalPort: 1080, Key: "interop-key", ASCII: "entropy", AEAD: "chacha20-poly1305", EnablePureDownlink: pure, HTTPMask: cfg.HTTPMaskConfig{Disable: true}}
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			c.ServerAddress = ln.Addr().String()
			if e = c.Finalize(); e != nil {
				t.Fatal(e)
			}
			table := codec.NewTable(c.Key, c.ASCII)
			go func() {
				for {
					raw, e := ln.Accept()
					if e != nil {
						return
					}
					go func() {
						defer raw.Close()
						s, _, e := tunnel.HandshakeAndUpgradeWithTablesMeta(raw, c, []*codec.Table{table})
						if e != nil {
							return
						}
						defer s.Close()
						msg, e := tunnel.ReadKIPMessage(s)
						if e != nil {
							return
						}
						if msg.Type == tunnel.KIPTypeOpenTCP {
							io.Copy(s, s)
						} else if msg.Type == tunnel.KIPTypeStartUoT {
							for {
								a, b, e := tunnel.ReadUoTDatagram(s)
								if e != nil {
									return
								}
								if tunnel.WriteUoTDatagram(s, a, b) != nil {
									return
								}
							}
						}
					}()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			h := &Outbound{ctx: ctx, cancel: cancel, base: tunnel.BaseDialer{Config: c, Tables: []*codec.Table{table}, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, n, a)
			}}}
			defer h.Close()
			for _, n := range []string{"tcp", "udp"} {
				conn, e := h.DialContext(ctx, n, M.ParseSocksaddr("example.org:443"))
				if e != nil {
					t.Fatal(e)
				}
				paneltest.Echo(t, conn)
			}
		})
	}
}

var _ = option.SudokuOutboundOptions{}

func TestCloseOwnsActiveConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Outbound{ctx: ctx, cancel: cancel}
	client, peer := net.Pipe()
	defer peer.Close()
	c, err := h.own(client)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 1)); done <- err }()
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read survived shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown left active read blocked")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	client2, peer2 := net.Pipe()
	defer peer2.Close()
	if _, err := h.own(client2); err != net.ErrClosed {
		t.Fatalf("registration after close: %v", err)
	}
}
