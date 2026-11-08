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

type hHandshakeFailureConn struct {
	net.Conn
	closes  int
	failure error
}

func (c *hHandshakeFailureConn) Read([]byte) (int, error) {
	return 0, c.failure
}

func (c *hHandshakeFailureConn) Write([]byte) (int, error) {
	return 0, c.failure
}

func (c *hHandshakeFailureConn) Close() error {
	c.closes++
	return nil
}

type hHandshakeFailureDialer struct {
	N.Dialer
	conn net.Conn
}

func (d hHandshakeFailureDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return d.conn, nil
}

func TestH_VLESSEncryptionHandshakeCloseOnce(t *testing.T) {
	for _, mode := range []string{"uninitialized", "transport-failure"} {
		for _, network := range []string{"tcp", "udp", "packet"} {
			t.Run(mode+"/"+network, func(t *testing.T) {
				failure := errors.New("transport handshake failure")
				raw := &hHandshakeFailureConn{failure: failure}
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
				h := &vlessDialer{logger: logger.NOP(), dialer: hHandshakeFailureDialer{conn: raw}, encryption: client}
				destination := M.ParseSocksaddrHostPort("example.invalid", 443)
				var err error
				if network == "packet" {
					conn, dialErr := h.ListenPacket(context.Background(), destination)
					err = dialErr
					if conn != nil {
						t.Fatal("failure returned a packet connection")
					}
				} else {
					conn, dialErr := h.DialContext(context.Background(), network, destination)
					err = dialErr
					if conn != nil {
						t.Fatal("failure returned a connection")
					}
				}
				if err == nil || !strings.Contains(err.Error(), "encryption handshake") {
					t.Fatalf("unexpected error: %v", err)
				}
				if mode == "transport-failure" && !errors.Is(err, failure) {
					t.Fatalf("original transport error lost: %v", err)
				}
				if raw.closes != 1 {
					t.Fatalf("raw transport closed %d times, want once", raw.closes)
				}
			})
		}
	}
}
