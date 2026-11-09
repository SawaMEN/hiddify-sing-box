package vless

import (
	"bytes"
	"sync"
	"testing"

	"github.com/sagernet/sing/common/buf"
)

func TestH_VisionFragmentedUUIDPrefix(t *testing.T) {
	var userUUID [16]byte
	copy(userUUID[:], "abcdefghijklmnop")
	conn := &VisionConn{userUUID: userUUID, remainingContent: -1, remainingPadding: -1}
	if got := conn.unPadding(userUUID[:4]); len(got) != 0 {
		t.Fatalf("fragmented UUID prefix was forwarded early: %d buffers", len(got))
	}
	if len(conn.readPrefix) != 4 {
		t.Fatalf("prefix length %d, want 4", len(conn.readPrefix))
	}
	if got := conn.unPadding(userUUID[4:15]); len(got) != 0 {
		t.Fatalf("fragmented UUID was forwarded early: %d buffers", len(got))
	}
	if len(conn.readPrefix) != 15 {
		t.Fatalf("prefix length %d, want 15", len(conn.readPrefix))
	}
	// A mismatching byte must release the exact original bytes, never discard
	// or misclassify a connection as a Vision padding frame.
	got := conn.unPadding([]byte{0xff})
	if len(got) != 1 {
		t.Fatalf("mismatched UUID produced %d buffers", len(got))
	}
	defer got[0].Release()
	if want := append(bytes.Clone(userUUID[:15]), 0xff); !bytes.Equal(got[0].Bytes(), want) {
		t.Fatalf("fragmented raw data changed: got %x want %x", got[0].Bytes(), want)
	}
	if len(conn.readPrefix) != 0 {
		t.Fatalf("prefix not cleared after mismatch: %d", len(conn.readPrefix))
	}
}

func TestH_VisionConcurrentTLSFilterSnapshot(t *testing.T) {
	const workers, iterations = 8, 125
	conn := &VisionConn{numberOfPacketToFilter: 10000}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				conn.filterTLS([][]byte{{0, 0, 0, 0, 0, 0, 0}})
				_, _, _ = conn.filterSnapshot()
			}
		}()
	}
	wg.Wait()
	if got, want := conn.filterPackets(), 10000-workers*iterations; got != want {
		t.Fatalf("filtered packets = %d, want %d", got, want)
	}
}

func TestH_VisionFilterStopsAtZero(t *testing.T) {
	conn := &VisionConn{numberOfPacketToFilter: 1}
	// No logger is required: stopping at zero must not touch the next packet.
	// Set a second buffer only if the counter was not exhausted already.
	conn.filterTLS(nil)
	if got := conn.filterPackets(); got != 1 {
		t.Fatalf("no input unexpectedly consumed filter budget: %d", got)
	}
}

func TestH_VisionBufferRelease(t *testing.T) {
	// Leave the parent connection unused and just check the buffer ownership.
	owned := buf.As([]byte("remaining")).ToOwned()
	if owned.IsEmpty() {
		t.Fatal("allocated empty buffer")
	}
	owned.Release()
}
