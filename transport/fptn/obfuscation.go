// Wire-compatible implementation of FPTN TlsObfuscator2 (FPTN 0.4.6).
// Protocol reference: fptn-project/fptn, MIT, Copyright 2024–2026 Stas Skokov.
package fptn

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

type obfuscatedConn struct {
	net.Conn
	enabled atomic.Bool
	pending []byte
}

func newObfuscatedConn(c net.Conn) *obfuscatedConn {
	r := &obfuscatedConn{Conn: c}
	r.enabled.Store(true)
	return r
}
func (c *obfuscatedConn) Write(p []byte) (int, error) {
	if !c.enabled.Load() {
		return c.Conn.Write(p)
	}
	total := 0
	for len(p) > 0 {
		n := min(len(p), 8175)
		var random [3]byte
		if _, e := rand.Read(random[:]); e != nil {
			return total, e
		}
		padding := 4095 + int(binary.BigEndian.Uint16(random[:2]))%4098
		b := make([]byte, 22+n+padding)
		if _, e := rand.Read(b); e != nil {
			return total, e
		}
		b[0] = 0x17
		b[1] = 3
		b[2] = 3
		binary.BigEndian.PutUint16(b[3:5], uint16(len(b)-5))
		binary.BigEndian.PutUint32(b[13:17], uint32(time.Now().Unix()))
		b[17] = random[2]
		binary.BigEndian.PutUint16(b[18:20], uint16(n))
		binary.BigEndian.PutUint16(b[20:22], uint16(padding))
		for i := range n {
			b[22+i] = p[i] ^ b[17]
		}
		if e := writeFull(c.Conn, b); e != nil {
			return total, e
		}
		total += n
		p = p[n:]
	}
	return total, nil
}
func (c *obfuscatedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	if !c.enabled.Load() {
		return c.Conn.Read(p)
	}
	var hdr [5]byte
	if _, e := io.ReadFull(c.Conn, hdr[:]); e != nil {
		return 0, e
	}
	size := int(binary.BigEndian.Uint16(hdr[3:]))
	if size > 32768 || size < 17 || hdr[0] != 0x17 || hdr[1] != 3 || hdr[2] != 3 {
		return 0, fmt.Errorf("fptn: invalid obfuscation record")
	}
	b := make([]byte, size)
	if _, e := io.ReadFull(c.Conn, b); e != nil {
		return 0, e
	}
	stamp := int64(binary.BigEndian.Uint32(b[8:12]))
	if delta := time.Now().Unix() - stamp; delta < -120 || delta > 120 {
		return 0, fmt.Errorf("fptn: invalid obfuscation timestamp")
	}
	n := int(binary.BigEndian.Uint16(b[13:15]))
	padding := int(binary.BigEndian.Uint16(b[15:17]))
	if n == 0 || n+padding+17 != size {
		return 0, fmt.Errorf("fptn: invalid obfuscation lengths")
	}
	for i := range n {
		b[17+i] ^= b[12]
	}
	c.pending = b[17 : 17+n]
	return c.Read(p)
}
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, e := w.Write(p)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
