package fptn

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	utls "github.com/metacubex/utls"
	"net"
	"time"
)

func sessionID(offset int) ([]byte, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	var stamp [4]byte
	binary.BigEndian.PutUint32(stamp[:], uint32(time.Now().Unix()))
	sum := sha1.Sum(stamp[:])
	copy(b[offset:offset+4], sum[:4])
	return b, nil
}
func setSession(c *utls.UConn, offset int) error {
	if e := c.BuildHandshakeState(); e != nil {
		return e
	}
	id, e := sessionID(offset)
	if e != nil {
		return e
	}
	c.HandshakeState.Hello.SessionId = id
	for _, e := range c.Extensions {
		if a, ok := e.(*utls.ALPNExtension); ok {
			a.AlpnProtocols = []string{"http/1.1"}
		}
	}
	return c.MarshalClientHello()
}

// decoyHandshake sends a browser ClientHello bearing FPTN's offset-14 marker,
// drains the server flight, then switches at the CCS boundary as upstream does.
func decoyHandshake(ctx context.Context, raw net.Conn, sni string) error {
	c := utls.UClient(raw, &utls.Config{ServerName: sni, InsecureSkipVerify: true}, utls.HelloChrome_Auto)
	if e := setSession(c, 14); e != nil {
		return e
	}
	hello := c.HandshakeState.Hello.Raw
	record := make([]byte, 5+len(hello))
	copy(record, []byte{22, 3, 1})
	binary.BigEndian.PutUint16(record[3:5], uint16(len(hello)))
	copy(record[5:], hello)
	if e := writeFull(raw, record); e != nil {
		return e
	}
	deadline := time.Now().Add(6 * time.Second)
	var data []byte
	complete := false
	for {
		if complete {
			raw.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		} else {
			raw.SetReadDeadline(deadline)
		}
		buf := make([]byte, 4096)
		n, e := raw.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > 65536 {
				return fmt.Errorf("fptn: oversized decoy response")
			}
			if len(data) >= 5 && data[0] == 22 && len(data) >= 5+int(binary.BigEndian.Uint16(data[3:5])) {
				complete = true
			}
		}
		if e != nil {
			if timeout, ok := e.(net.Error); ok && timeout.Timeout() && complete {
				break
			}
			return fmt.Errorf("fptn: decoy handshake failed")
		}
	}
	raw.SetReadDeadline(time.Time{})
	if e := writeFull(raw, []byte{20, 3, 3, 0, 1, 1}); e != nil {
		return e
	}
	return transitionPause(ctx)
}
func DialTLS(ctx context.Context, raw net.Conn, sni, fingerprint, bypass string) (net.Conn, error) {
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	raw.SetDeadline(time.Now().Add(20 * time.Second))
	if bypass == "sni-spoofing" {
		if e := decoyHandshake(ctx, raw, sni); e != nil {
			raw.Close()
			return nil, e
		}
	}
	wire := newObfuscatedConn(raw)
	conf := &utls.Config{ServerName: sni, MinVersion: utls.VersionTLS12, InsecureSkipVerify: true, VerifyPeerCertificate: func(certs [][]byte, _ [][]*x509.Certificate) error {
		if len(certs) == 0 {
			return fmt.Errorf("fptn: missing server certificate")
		}
		sum := md5.Sum(certs[0])
		if hex.EncodeToString(sum[:]) != fingerprint {
			return fmt.Errorf("fptn: certificate fingerprint mismatch")
		}
		return nil
	}}
	c := utls.UClient(wire, conf, utls.HelloChrome_Auto)
	if e := setSession(c, 28); e != nil {
		raw.Close()
		return nil, e
	}
	if e := c.HandshakeContext(ctx); e != nil {
		raw.Close()
		return nil, fmt.Errorf("fptn: TLS handshake failed")
	}
	wire.enabled.Store(false)
	if e := transitionPause(ctx); e != nil {
		raw.Close()
		return nil, e
	}
	raw.SetDeadline(time.Time{})
	if ctx.Err() != nil {
		raw.Close()
		return nil, ctx.Err()
	}
	return c, nil
}

// The upstream server switches its stream wrapper between handshake phases.
func transitionPause(ctx context.Context) error {
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
