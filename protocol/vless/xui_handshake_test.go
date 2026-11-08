package vless

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/protocol/vless/encryption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type xuiHandshakeConn struct {
	net.Conn
	closes  int
	failure error
}

func (c *xuiHandshakeConn) Read([]byte) (int, error)  { return 0, c.failure }
func (c *xuiHandshakeConn) Write([]byte) (int, error) { return 0, c.failure }
func (c *xuiHandshakeConn) Close() error              { c.closes++; return nil }

type xuiHandshakeDialer struct {
	N.Dialer
	conn net.Conn
}

func (d xuiHandshakeDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

// Failed subscription probes must return an error and release the transport,
// rather than closing a typed-nil *encryption.CommonConn and crashing the core.
func TestXUIPanelEncryptionHandshakeFailure(t *testing.T) {
	for _, mode := range []string{"uninitialized", "transport-failure"} {
		for _, network := range []string{"tcp", "udp", "packet"} {
			t.Run(mode+"/"+network, func(t *testing.T) {
				failure := errors.New("test transport closed")
				raw := &xuiHandshakeConn{failure: failure}
				client := &encryption.ClientInstance{}
				if mode == "transport-failure" {
					key, err := ecdh.X25519().GenerateKey(rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					if err := client.Init([][]byte{key.PublicKey().Bytes()}, 0, 0, ""); err != nil {
						t.Fatal(err)
					}
				}
				h := &vlessDialer{logger: logger.NOP(), dialer: xuiHandshakeDialer{conn: raw}, encryption: client}
				destination := M.ParseSocksaddrHostPort("example.invalid", 443)
				var err error
				if network == "packet" {
					conn, dialErr := h.ListenPacket(context.Background(), destination)
					err = dialErr
					if conn != nil {
						t.Fatal("failed handshake returned a packet connection")
					}
				} else {
					conn, dialErr := h.DialContext(context.Background(), network, destination)
					err = dialErr
					if conn != nil {
						t.Fatal("failed handshake returned a connection")
					}
				}
				if err == nil || !strings.Contains(err.Error(), "encryption handshake") {
					t.Fatalf("unexpected error: %v", err)
				}
				if mode == "transport-failure" && !errors.Is(err, failure) {
					t.Fatalf("transport error lost: %v", err)
				}
				if raw.closes != 1 {
					t.Fatalf("transport closed %d times, want exactly once", raw.closes)
				}
			})
		}
	}
}
