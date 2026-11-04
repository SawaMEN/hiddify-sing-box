package v2rayxdrive

import (
	"net"
	"sync"
)

// Keep transport ownership when the canonical storage implementation creates the connections.
type hTrackedConn struct {
	net.Conn
	owner *Client
	once  sync.Once
	err   error
}

func (c *hTrackedConn) Upstream() any { return c.Conn }

func (c *hTrackedConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.owner.access.Lock()
		delete(c.owner.connections, c)
		c.owner.access.Unlock()
	})
	return c.err
}

func (c *Client) track(conn net.Conn) (net.Conn, error) {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		conn.Close()
		return nil, net.ErrClosed
	}
	tracked := &hTrackedConn{Conn: conn, owner: c}
	c.connections[tracked] = struct{}{}
	return tracked, nil
}

func (c *Client) closeConnections() error {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		return nil
	}
	c.closed = true
	c.cancel()
	connections := make([]*hTrackedConn, 0, len(c.connections))
	for conn := range c.connections {
		connections = append(connections, conn)
	}
	c.access.Unlock()
	for _, conn := range connections {
		conn.Close()
	}
	return nil
}
