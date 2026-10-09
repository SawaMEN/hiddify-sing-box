package fptn

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestSessionIDMarker(t *testing.T) {
	for _, offset := range []int{14, 28} {
		id, e := sessionID(offset)
		if e != nil {
			t.Fatal(e)
		}
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(time.Now().Unix()))
		hash := sha1.Sum(b[:])
		if len(id) != 32 || !bytes.Equal(id[offset:offset+4], hash[:4]) {
			t.Fatal("FPTN SHA1 timestamp marker mismatch")
		}
	}
}
func TestTLS2WireRecord(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() { c := newObfuscatedConn(a); c.Write([]byte("fptn")) }()
	var h [22]byte
	if _, e := io.ReadFull(b, h[:]); e != nil {
		t.Fatal(e)
	}
	payload := int(binary.BigEndian.Uint16(h[18:20]))
	padding := int(binary.BigEndian.Uint16(h[20:22]))
	if payload != 4 || padding < 4095 || padding > 8192 || int(binary.BigEndian.Uint16(h[3:5])) != 17+payload+padding || !bytes.Equal(h[:3], []byte{23, 3, 3}) {
		t.Fatal("wire does not match packed C++ TLSAppDataRecordHeader")
	}
	body := make([]byte, payload+padding)
	io.ReadFull(b, body)
	for i := range payload {
		body[i] ^= h[17]
	}
	if string(body[:payload]) != "fptn" {
		t.Fatal("XOR payload mismatch")
	}
}
