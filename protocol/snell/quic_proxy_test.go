package snell

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"os"
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
	if err != nil {
		t.Fatal(err)
	}
	gotDest, gotKey, gotInitial, err := decodeQUICProxyPacket(psk, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if gotDest != dest || gotKey != string(userKey) || !bytes.Equal(initial, gotInitial) {
		t.Fatalf("QUIC proxy envelope mismatch: %s/%q", gotDest, gotKey)
	}
}

func TestSnellV5QUICProxyRejectsCorruption(t *testing.T) {
	psk := []byte("snell-v5-test-psk")
	dest := M.ParseSocksaddrHostPort("example.org", 443)
	wrapped, err := encodeQUICProxyPacket(psk, nil, dest, testQUICInitial())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		packet []byte
		key    []byte
	}{
		{"too-short", []byte{1, 2, 3}, psk},
		{"wrong-key", wrapped, []byte("other-secret")},
		{"tamper", func() []byte { cloned := bytes.Clone(wrapped); cloned[len(cloned)-1] ^= 0xff; return cloned }(), psk},
		{"truncated", wrapped[:len(wrapped)-30], psk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := decodeQUICProxyPacket(tc.key, tc.packet)
			if err == nil {
				t.Fatal("unauthenticated QUIC packet accepted")
			}
		})
	}
}

func TestSnellV5QUICProxyRejectsNonQUIC(t *testing.T) {
	if isQUICInitial([]byte{0x40, 0, 0, 0, 1}) {
		t.Fatal("short-header QUIC is not a new Initial")
	}
	dest := M.ParseSocksaddrHostPort("example.org", 443)
	if _, err := encodeQUICProxyPacket([]byte("snell-v5-test-psk"), nil, dest, []byte{1, 2, 3}); err == nil {
		t.Fatal("non-QUIC initial accepted")
	}
}

func TestSnellQUICProxyUserIdentity(t *testing.T) {
	single := &quicProxyServer{users: map[string]string{"key-a": "alice"}}
	if name, ok := single.matchQUICUser(""); !ok || name != "alice" {
		t.Fatalf("single-user Surge fallback should be accounted as alice: %q, %v", name, ok)
	}
	multi := &quicProxyServer{users: map[string]string{"key-a": "alice", "key-b": "bob"}}
	if _, ok := multi.matchQUICUser(""); ok {
		t.Fatal("PSK-only QUIC bypassed multi-user authentication")
	}
	if name, ok := multi.matchQUICUser("key-b"); !ok || name != "bob" {
		t.Fatalf("authenticated QUIC user mismatch: %q, %v", name, ok)
	}
	if _, ok := multi.matchQUICUser("unknown"); ok {
		t.Fatal("unrecognized QUIC user was accepted")
	}
}

func TestSnellV5SurgeCapture(t *testing.T) {
	packet, err := os.ReadFile("testdata/surge-v5-initial.bin")
	if err != nil {
		t.Fatal(err)
	}
	dest, key, inner, err := decodeQUICProxyPacket([]byte("JFdZLmZLtYtUP308QrqdoA=="), packet)
	if err != nil {
		t.Fatal(err)
	}
	if dest.String() != "www.cloudflare.com:443" || key != "" || len(inner) != 1280 || !isQUICInitial(inner) {
		t.Fatalf("incompatible capture: %s %q %d", dest, key, len(inner))
	}
}
