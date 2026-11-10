// Package xuispeed connects native endpoints to the optional per-client speed broker.
package xuispeed

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const (
	Environment = "XUI_SPEED_BROKER"
	Version     = "xui-speed-broker-v1"
)

func init() {
	if len(os.Args) == 2 && os.Args[1] == "--xui-speed-broker-version" {
		_, _ = io.WriteString(os.Stdout, Version+"\n")
		os.Exit(0)
	}
}

type Config struct {
	Address  string            `json:"address"`
	Password string            `json:"password"`
	Users    map[string]string `json:"users"`
}

type Client struct {
	config Config
	user   string
	mu     sync.Mutex
	closed bool
	lanes  [2]*lane
}
type lane struct {
	sync.Mutex
	conn net.Conn
}

func New(user string) (*Client, error) {
	value := os.Getenv(Environment)
	if value == "" {
		return nil, nil
	}
	data, err := os.ReadFile(value)
	if err != nil {
		return nil, errors.New("speed broker configuration unavailable")
	}
	var cfg Config
	if json.Unmarshal(data, &cfg) != nil {
		return nil, errors.New("invalid speed broker configuration")
	}
	return NewWithConfig(cfg, user)
}

func NewWithConfig(cfg Config, user string) (*Client, error) {
	id, limited := cfg.Users[user]
	if !limited {
		return nil, nil
	}
	host, _, err := net.SplitHostPort(cfg.Address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || len(cfg.Password) == 0 || len(cfg.Password) > 255 || len(id) == 0 || len(id) > 255 {
		return nil, errors.New("invalid speed broker endpoint")
	}
	return &Client{config: cfg, user: id, lanes: [2]*lane{{}, {}}}, nil
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, l := range c.lanes {
		if l.conn != nil {
			_ = l.conn.Close()
		}
	}
}

func (c *Client) Wait(ctx context.Context, direction byte, n int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || n == 0 {
		return nil
	}
	if direction > 1 || n < 0 {
		return errors.New("invalid speed request")
	}
	l := c.lanes[direction]
	l.Lock()
	defer l.Unlock()
	for n > 0 {
		chunk := chunkSize(n)
		if err := c.wait(ctx, l, direction, chunk); err != nil {
			c.Close()
			return err
		}
		n -= chunk
	}
	return nil
}

func (c *Client) wait(ctx context.Context, l *lane, direction byte, n int) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return net.ErrClosed
	}
	conn := l.conn
	c.mu.Unlock()
	if conn == nil {
		var err error
		conn, err = (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", c.config.Address)
		if err != nil {
			return err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = conn.Close()
			return net.ErrClosed
		}
		l.conn = conn
		c.mu.Unlock()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err = c.authenticate(conn); err != nil {
			return err
		}
	}
	_ = conn.SetDeadline(time.Now().Add(35 * time.Second))
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	frame := []byte{direction, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(frame[1:], uint32(n))
	if err := writeAll(conn, frame); err != nil {
		return err
	}
	var reply [1]byte
	_, err := io.ReadFull(conn, reply[:])
	if err == nil && reply[0] != 0 {
		return errors.New("speed broker rejected budget")
	}
	return err
}

func (c *Client) authenticate(conn net.Conn) error {
	if err := writeAll(conn, []byte{5, 1, 2}); err != nil {
		return err
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply != [2]byte{5, 2} {
		return errors.New("speed broker authentication required")
	}
	auth := append([]byte{1, byte(len(c.user))}, c.user...)
	auth = append(auth, byte(len(c.config.Password)))
	auth = append(auth, c.config.Password...)
	if err := writeAll(conn, auth); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return err
	}
	if reply != [2]byte{1, 0} {
		return errors.New("speed broker authentication failed")
	}
	if err := writeAll(conn, []byte{5, 0xf0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var response [10]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return err
	}
	if response[0] != 5 || response[1] != 0 {
		return errors.New("speed broker unavailable")
	}
	return nil
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

type limitedConn struct {
	net.Conn
	client *Client
}

func Wrap(conn net.Conn, user string) (net.Conn, error) {
	client, err := New(user)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return conn, nil
	}
	return &limitedConn{conn, client}, nil
}

func (c *limitedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if waitErr := c.client.Wait(context.Background(), 0, n); waitErr != nil {
		_ = c.Close()
		return 0, waitErr
	}
	return n, err
}

func (c *limitedConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := chunkSize(len(p))
		if err := c.client.Wait(context.Background(), 1, n); err != nil {
			_ = c.Close()
			return total, err
		}
		sent, err := c.Conn.Write(p[:n])
		total += sent
		if err != nil {
			return total, err
		}
		if sent != n {
			return total, io.ErrShortWrite
		}
		p = p[n:]
	}
	return total, nil
}
func (c *limitedConn) Close() error { c.client.Close(); return c.Conn.Close() }
func (c *limitedConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return c.Close()
}

func (c *limitedConn) CloseRead() error {
	if conn, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return nil
}

func chunkSize(n int) int {
	if n > 32768 {
		return 32768
	}
	return n
}
