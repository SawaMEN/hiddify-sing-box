package vless

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestXUIPanelPreconnectLifecycle(t *testing.T) {
	var calls atomic.Int32
	peers := make(chan net.Conn, 16)
	pool := newPreconnectPool(context.Background(), 2, func(context.Context) (net.Conn, net.Conn, error) {
		calls.Add(1)
		a, b := net.Pipe()
		peers <- b
		return a, a, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if calls.Load() != 0 {
		t.Fatal("pool dialed before the first request")
	}
	first, base, err := pool.Get(ctx)
	if err != nil || first != base {
		t.Fatalf("get: %v", err)
	}
	_ = first.Close()
	pool.Reset()
	second, _, err := pool.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
	pool.Close()
	if conn, _, err := pool.Get(ctx); err == nil {
		_ = conn.Close()
		t.Fatal("closed pool returned a connection")
	}
	close(peers)
	for peer := range peers {
		_ = peer.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err := peer.Read(b[:]); err == nil {
			t.Fatal("pool leaked a peer connection")
		}
		_ = peer.Close()
	}
}

func TestXUIPanelPreconnectCloseBeforeGet(t *testing.T) {
	pool := newPreconnectPool(context.Background(), 1, func(context.Context) (net.Conn, net.Conn, error) {
		t.Error("dial after close")
		return nil, nil, context.Canceled
	})
	pool.Close()
	if _, _, err := pool.Get(context.Background()); err == nil {
		t.Fatal("closed pool accepted Get")
	}
}
