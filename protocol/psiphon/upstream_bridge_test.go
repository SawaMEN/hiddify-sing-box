package psiphon

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func bridgeTestConnect(t *testing.T, b *upstreamBridge, auth string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	u, err := url.Parse(b.url)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: "target.invalid:443"}, Host: "target.invalid:443", Header: make(http.Header)}
	request.Header.Set("Proxy-Authorization", auth)
	if err := request.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		t.Fatal(err)
	}
	return conn, reader, response
}

func TestUpstreamBridgeUsesOnlyConfiguredDialer(t *testing.T) {
	var calls atomic.Int32
	b, err := newUpstreamBridge(context.Background(), func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if network != "tcp" || address != "target.invalid:443" {
			t.Errorf("unexpected destination %s %s", network, address)
		}
		client, server := net.Pipe()
		go func() { defer server.Close(); _, _ = io.Copy(server, server) }()
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	conn, reader, response := bridgeTestConnect(t, b, b.authorization)
	defer conn.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if _, err := conn.Write([]byte("tunnel")); err != nil {
		t.Fatal(err)
	}
	output := make([]byte, 6)
	if _, err := io.ReadFull(reader, output); err != nil {
		t.Fatal(err)
	}
	if string(output) != "tunnel" || calls.Load() != 1 {
		t.Fatalf("unexpected %q, calls %d", output, calls.Load())
	}
}

func TestUpstreamBridgeRejectsUnauthenticatedConnections(t *testing.T) {
	var calls atomic.Int32
	b, err := newUpstreamBridge(context.Background(), func(context.Context, string, string) (net.Conn, error) { calls.Add(1); return nil, net.ErrClosed })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, auth := range []string{"", "Basic wrong"} {
		conn, _, response := bridgeTestConnect(t, b, auth)
		response.Body.Close()
		conn.Close()
		if response.StatusCode != 407 {
			t.Fatal(response.Status)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated caller reached dialer")
	}
}

func TestUpstreamBridgeCloseCancelsPendingDialAndClosesListener(t *testing.T) {
	dialStarted := make(chan struct{})
	dialStopped := make(chan struct{})
	b, err := newUpstreamBridge(context.Background(), func(ctx context.Context, _ string, _ string) (net.Conn, error) {
		close(dialStarted)
		<-ctx.Done()
		close(dialStopped)
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(b.url)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: "target.invalid:443"}, Host: "target.invalid:443", Header: make(http.Header)}
	request.Header.Set("Proxy-Authorization", b.authorization)
	if err := request.Write(conn); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("dial not reached")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialStopped:
	case <-time.After(time.Second):
		t.Fatal("dial not cancelled")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	late, err := net.DialTimeout("tcp", u.Host, time.Second)
	if err == nil {
		late.Close()
		t.Fatal("listener survived Close")
	}
}

func TestUpstreamBridgeCloseTerminatesEstablishedTunnel(t *testing.T) {
	b, err := newUpstreamBridge(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { defer server.Close(); _, _ = io.Copy(io.Discard, server) }()
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	conn, reader, response := bridgeTestConnect(t, b, b.authorization)
	defer conn.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection survived close")
	}
}
