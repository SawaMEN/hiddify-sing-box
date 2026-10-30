// SPDX-License-Identifier: MPL-2.0
package xdrive

import (
	"context"
	"github.com/sagernet/sing-box/option"
	splithttp "github.com/sagernet/sing-box/transport/v2rayxhttp"
	"github.com/xtls/xray-core/common/errors"
	"io"
	"net/http"
	"sync"
	"time"
)

var errSessionClosed = errors.New("the session is closed")

// connPool hands the sessions of a storage the connections of XMUX, each a
// client of its own made by newClient
type connPool struct {
	mu   sync.Mutex
	xmux *splithttp.XmuxManager
}

func newConnPool(config *option.V2RayXHTTPXmuxOptions, newClient func(connCtx context.Context) *http.Client) *connPool {
	var xmuxConfig option.V2RayXHTTPXmuxOptions
	if config != nil {
		xmuxConfig = *config
	}
	return &connPool{xmux: splithttp.NewXmuxManager(xmuxConfig, func() splithttp.XmuxConn {
		ctx, cancel := context.WithCancel(context.Background())
		return &poolConn{next: newClient(ctx).Transport, cancel: cancel}
	})}
}

// poolConn is a connection of XMUX, which closes once XMUX drops it and the
// requests on it end
type poolConn struct {
	next   http.RoundTripper
	cancel context.CancelFunc

	mu       sync.Mutex
	inflight int
	closing  bool
}

func (c *poolConn) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closing
}

func (c *poolConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closing = true
	if c.inflight == 0 {
		c.cancel()
	}
	return nil
}

func (c *poolConn) done() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inflight--
	if c.closing && c.inflight == 0 {
		c.cancel()
	}
}

// roundTrip sends a request counted in inflight by the caller
func (c *poolConn) roundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(r)
	if err != nil {
		c.done()
		return nil, err
	}
	resp.Body = &doneBody{ReadCloser: resp.Body, done: sync.OnceFunc(c.done)}
	return resp, nil
}

type doneBody struct {
	io.ReadCloser
	done func()
}

func (b *doneBody) Close() error {
	err := b.ReadCloser.Close()
	b.done()
	return err
}

// sessionTransport sends the requests of one session over a connection of
// XMUX, and over another once that one has no requests or time left
type sessionTransport struct {
	pool *connPool

	mu     sync.Mutex
	client *splithttp.XmuxClient
	closed bool
}

func (t *sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errSessionClosed
	}
	client := t.client
	if client == nil || client.LeftRequests.Add(-1) < 0 || !client.UnreusableAt.IsZero() && time.Now().After(client.UnreusableAt) {
		t.pool.mu.Lock()
		t.client = t.pool.xmux.GetXmuxClient(r.Context())
		t.pool.mu.Unlock()
		t.client.OpenUsage.Add(1)
		t.client.LeftRequests.Add(-1)
		if client != nil {
			if client.OpenUsage.Add(-1) == 0 {
				client.XmuxConn.(*poolConn).Close()
			}
		}
		client = t.client
	}
	// counted under mu, or another request of the session that moves on could
	// close the connection before this one is sent
	conn := client.XmuxConn.(*poolConn)
	conn.mu.Lock()
	conn.inflight++
	conn.mu.Unlock()
	t.mu.Unlock()
	return conn.roundTrip(r)
}

func (t *sessionTransport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.client != nil {
		if t.client.OpenUsage.Add(-1) == 0 {
			t.client.XmuxConn.(*poolConn).Close()
		}
		t.client = nil
	}
}
