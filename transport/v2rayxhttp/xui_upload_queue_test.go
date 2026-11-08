package xhttp

import (
	"testing"
)

func TestXUIPanelUploadQueueZeroCapacityAndRead(t *testing.T) {
	queue := NewUploadQueue(0)
	defer queue.Close()
	if queue.maxPackets != 1 || cap(queue.pushedPackets) != 1 {
		t.Fatalf("zero-capacity queue not clamped: max=%d cap=%d", queue.maxPackets, cap(queue.pushedPackets))
	}
	if n, err := queue.Read(nil); n != 0 || err != nil {
		t.Fatalf("zero-length read must not block or consume packets: %d %v", n, err)
	}
	if err := queue.Push(Packet{Seq: 0, Payload: []byte("a")}); err != nil {
		t.Fatal(err)
	}
	if n, err := queue.Read([]byte{}); n != 0 || err != nil {
		t.Fatalf("empty read unexpectedly consumed packet: %d %v", n, err)
	}
	buf := make([]byte, 1)
	n, err := queue.Read(buf)
	if err != nil || n != 1 || string(buf[:n]) != "a" {
		t.Fatalf("packet lost after zero-length read: %q %d %v", buf, n, err)
	}
}
