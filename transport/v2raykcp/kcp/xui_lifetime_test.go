package kcp_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/sagernet/sing-box/transport/v2raykcp/kcp"

	"github.com/xtls/xray-core/common/buf"
)

type countedKCPCloser struct {
	count atomic.Int32
}

func (c *countedKCPCloser) Close() error {
	c.count.Add(1)
	return nil
}

func newPanelKCPConnection(closer *countedKCPCloser) *Connection {
	return NewConnection(ConnMetadata{Conversation: 88}, buf.DiscardBytes, closer, &Config{
		Mtu:              1350,
		Tti:              50,
		UplinkCapacity:   5,
		DownlinkCapacity: 20,
		CwndMultiplier:   1,
		MaxSendingWindow: 2 * 1024 * 1024,
	})
}

func TestXUIPanelKCPTerminateOnlyOnce(t *testing.T) {
	closer := &countedKCPCloser{}
	conn := newPanelKCPConnection(closer)
	var group sync.WaitGroup
	for range 24 {
		group.Go(conn.Terminate)
	}
	group.Wait()
	if got := closer.count.Load(); got != 1 {
		t.Fatalf("underlying socket closed %d times, want once", got)
	}
	if conn.State() != StateTerminated {
		t.Fatal("terminated KCP connection revived")
	}
	conn.SetState(StateActive)
	if conn.State() != StateTerminated {
		t.Fatal("terminated KCP connection accepted a state transition")
	}
}

func TestXUIPanelKCPReadDeadlineInterruptsBlockedRead(t *testing.T) {
	closer := &countedKCPCloser{}
	conn := newPanelKCPConnection(closer)
	defer conn.Terminate()
	result := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 64))
		result <- err
	}()
	if err := conn.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != ErrIOTimeout {
			t.Fatalf("expected deadline error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not wake after a deadline update")
	}
}
