package snell

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

func testQUICInitial() []byte {
	initial := make([]byte, 1280)
	_, _ = rand.Read(initial)
	initial[0] = 0xc3
	binary.BigEndian.PutUint32(initial[1:5], 1)
	return initial
}

func TestSnellV5QUICProxyRoundTrip(t *testing.T) {
	psk := []byte("snell-v5-test-psk")
	userKey := []byte("client-key")
	initial := testQUICInitial()
	dest := M.ParseSocksaddrHostPort("example.org", 443)
	wrapped, err := encodeQUICProxyPacket(psk, userKey, dest, initial)
	if err != nil { t.Fatal(err) }
	gotDest, gotKey, gotInitial, err := decodeQUICProxyPacket(psk, wrapped)
	if err != nil { t.Fatal(err) }
	if gotDest != dest || gotKey != string(userKey) || !bytes.Equal(initial, gotInitial) {
		t.Fatalf("QUIC proxy envelope mismatch: %s/%q", gotDest, gotKey)
	}
}

func TestSnellV5QUICProxyRejectsCorruption(t *testing.T) {
	psk := []byte("snell-v5-test-psk")
	dest := M.ParseSocksaddrHostPort("example.org", 443)
	wrapped, err := encodeQUICProxyPacket(psk, nil, dest, testQUICInitial())
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct {
		name string
		packet []byte
		key []byte
	}{
		{"too-short", []byte{1,2,3}, psk},
		{"wrong-key", wrapped, []byte("other-secret")},
		{"tamper", func() []byte { cloned:=bytes.Clone(wrapped);cloned[len(cloned)-1]^=0xff;return cloned }(), psk},
		{"truncated", wrapped[:len(wrapped)-30], psk},
	} {
		t.Run(tc.name,func(t *testing.T) {
			_, _, _, err := decodeQUICProxyPacket(tc.key,tc.packet)
			if err == nil {t.Fatal("unauthenticated QUIC packet accepted")}
		})
	}
}

func TestSnellV5QUICProxyRejectsNonQUIC(t *testing.T) {
	if isQUICInitial([]byte{0x40,0,0,0,1}) {
		t.Fatal("short-header QUIC is not a new Initial")
	}
	dest := M.ParseSocksaddrHostPort("example.org",443)
	if _,err := encodeQUICProxyPacket([]byte("snell-v5-test-psk"),nil,dest,[]byte{1,2,3});err==nil {
		t.Fatal("non-QUIC initial accepted")
	}
}
