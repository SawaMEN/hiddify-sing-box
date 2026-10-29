package xdrive

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// HTTP requests use the native dialer, including its resolver, binding and
// detour. Never fall back to net.Dialer when a configured detour fails.
func newServiceClient(connCtx context.Context, _ *sync.Map, options *streamOptions, stall time.Duration, maxConns int) *http.Client {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		target := M.ParseSocksaddr(addr)
		if config, ok := options.ProtocolSettings.(*Config); ok && config.FrontAddress != "" {
			port := target.Port
			if config.FrontPort > 0 {
				port = config.FrontPort
			}
			target = M.ParseSocksaddrHostPort(config.FrontAddress, port)
		}
		conn, err := options.dialer.DialContext(ctx, N.NetworkTCP, target)
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(connCtx, func() { conn.Close() })
		stopBase := func() bool { return false }
		if options.ctx != nil {
			stopBase = context.AfterFunc(options.ctx, func() { conn.Close() })
		}
		return &ctxConn{Conn: conn, stop: func() bool { a := stop(); b := stopBase(); return a || b }}, nil
	}
	keepAlive := 45 * time.Second
	if config, ok := options.ProtocolSettings.(*Config); ok && config.Xmux != nil {
		if config.Xmux.HKeepAlivePeriod < 0 {
			keepAlive = 0
		} else if config.Xmux.HKeepAlivePeriod > 0 {
			keepAlive = time.Duration(config.Xmux.HKeepAlivePeriod) * time.Second
		}
	}
	transport := &http.Transport{
		HTTP2:       &http.HTTP2Config{SendPingTimeout: keepAlive},
		DialContext: dial, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: stall,
		ResponseHeaderTimeout: stall, MaxIdleConns: maxConns,
		MaxIdleConnsPerHost: maxConns, MaxConnsPerHost: maxConns,
	}
	options.mu.Lock()
	if options.clients == nil {
		options.clients = make(map[*http.Transport]struct{})
	}
	options.clients[transport] = struct{}{}
	options.mu.Unlock()
	context.AfterFunc(connCtx, func() {
		transport.CloseIdleConnections()
		options.mu.Lock()
		delete(options.clients, transport)
		options.mu.Unlock()
	})
	return &http.Client{Transport: &serviceTransport{plain: transport, secure: transport, stall: stall}}
}

type ctxConn struct {
	net.Conn
	stop func() bool
}

func (c *ctxConn) Close() error { c.stop(); return c.Conn.Close() }
