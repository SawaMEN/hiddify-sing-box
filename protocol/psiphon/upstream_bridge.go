package psiphon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Psiphon's controller accepts an upstream URL rather than sing-box's dialer.
// This private authenticated CONNECT bridge keeps bootstrap TCP sockets on the
// configured detour (and Android protection/root routing mark).
type upstreamBridge struct {
	url           string
	authorization string
	ctx           context.Context
	cancel        context.CancelFunc
	server        *http.Server
	dial          func(context.Context, string, string) (net.Conn, error)
	slots         chan struct{}
	mu            sync.Mutex
	closed        bool
	connections   map[net.Conn]struct{}
	done          chan struct{}
}

func newUpstreamBridge(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) (*upstreamBridge, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	password := hex.EncodeToString(secret)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	bridge := &upstreamBridge{
		url:           (&url.URL{Scheme: "http", Host: listener.Addr().String(), User: url.UserPassword("psiphon", password)}).String(),
		authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte("psiphon:"+password)),
		ctx:           ctx, cancel: cancel, dial: dial, slots: make(chan struct{}, 32),
		connections: make(map[net.Conn]struct{}), done: make(chan struct{}),
	}
	bridge.server = &http.Server{Handler: bridge, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	go func() { defer close(bridge.done); _ = bridge.server.Serve(listener) }()
	return bridge, nil
}

func (b *upstreamBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(b.authorization)) != 1 {
		w.Header().Set("Proxy-Authenticate", `Basic realm="psiphon"`)
		http.Error(w, "authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || host == "" || port == "" {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		http.Error(w, "bridge busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
	upstream, err := b.dial(ctx, "tcp", r.Host)
	cancel()
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unavailable", http.StatusInternalServerError)
		return
	}
	conn, reader, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.connections[conn] = struct{}{}
	b.connections[upstream] = struct{}{}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.connections, conn); delete(b.connections, upstream); b.mu.Unlock() }()
	_ = conn.SetDeadline(time.Time{})
	if _, err := reader.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := reader.Flush(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(upstream, reader); _ = upstream.Close(); _ = conn.Close() }()
	_, _ = io.Copy(conn, upstream)
	_ = conn.Close()
	_ = upstream.Close()
	<-done
}

func (b *upstreamBridge) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.cancel()
	connections := make([]net.Conn, 0, len(b.connections))
	for conn := range b.connections {
		connections = append(connections, conn)
	}
	b.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
	err := b.server.Close()
	<-b.done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
