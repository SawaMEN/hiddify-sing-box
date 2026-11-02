package xdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type Config = option.V2RayXDriveOptions

type streamOptions struct {
	ProtocolSettings any
	ctx              context.Context
	storageOnce      sync.Once
	storage          Storage
	storageErr       error
	dialer           N.Dialer
	mu               sync.Mutex
	clients          map[*http.Transport]struct{}
}

func (s *streamOptions) close() {
	s.storageOnce.Do(func() { s.storageErr = context.Canceled })
	if s.storage != nil {
		s.storage.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.CloseIdleConnections()
	}
	s.clients = nil
}

func validate(options Config) error {
	if strings.TrimSpace(options.RemoteFolder) == "" {
		return fmt.Errorf("XDrive remote_folder is required")
	}
	switch options.Service {
	case "local":
	case "google-drive", "Google Drive":
		if len(options.Secrets) != 3 {
			return fmt.Errorf("XDrive Google Drive requires client ID, client secret and refresh token")
		}
		for _, secret := range options.Secrets {
			if strings.TrimSpace(secret) == "" {
				return fmt.Errorf("XDrive Google Drive secrets must not be empty")
			}
		}
	case "template":
		if strings.TrimSpace(options.Template) == "" {
			return fmt.Errorf("XDrive storage template is required")
		}
		var template storageTemplate
		if err := json.Unmarshal([]byte(options.Template), &template); err != nil {
			return fmt.Errorf("invalid XDrive template: %w", err)
		}
		if template.Put.URL == "" || template.Get.URL == "" || template.List.URL == "" || template.Delete.URL == "" {
			return fmt.Errorf("XDrive template needs put, get, list and delete operations")
		}
		names, err := regexp.Compile(template.List.NamesRegex)
		if err != nil || names.NumSubexp() < 1 {
			return fmt.Errorf("XDrive template namesRegex needs a capture group")
		}

	case "s3":
		if options.S3 == nil || len(options.Secrets) < 2 || len(options.Secrets) > 3 {
			return fmt.Errorf("XDrive S3 requires endpoint, bucket and two or three secrets")
		}
	default:
		return fmt.Errorf("unsupported XDrive service %q", options.Service)
	}
	if options.SegmentBytes > maxSegmentBytes || options.Concurrency > maxConcurrency {
		return fmt.Errorf("XDrive segment_bytes must be <= 16777216 and concurrency <= 64")
	}
	return nil
}

type Server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	options  *streamOptions
	handler  adapter.V2RayServerTransportHandler
	listener *Listener
}

var _ adapter.V2RayServerTransport = (*Server)(nil)

func NewServer(ctx context.Context, _ logger.ContextLogger, options Config, tlsConfig tls.ServerConfig, handler adapter.V2RayServerTransportHandler) (*Server, error) {
	if tlsConfig != nil {
		return nil, fmt.Errorf("XDrive uses HTTPS storage and VLESS encryption; tunnel TLS must be disabled")
	}
	if err := validate(options); err != nil {
		return nil, err
	}
	var nativeDialer N.Dialer
	if options.Service != "local" {
		var err error
		nativeDialer, err = dialer.New(ctx, options.StorageDialer, true)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Server{ctx: ctx, cancel: cancel, options: &streamOptions{ProtocolSettings: &options, ctx: ctx, dialer: nativeDialer}, handler: handler}, nil
}

func (s *Server) Start() error {
	listener, err := listenSessions(s.ctx, s.options, func(conn net.Conn) {
		go s.handler.NewConnectionEx(log.ContextWithNewID(s.ctx), conn, M.Socksaddr{}, M.Socksaddr{}, nil)
	})
	s.listener = listener
	return err
}
func (s *Server) Network() []string                { return nil }
func (s *Server) Serve(net.Listener) error         { return os.ErrInvalid }
func (s *Server) ServePacket(net.PacketConn) error { return os.ErrInvalid }
func (s *Server) Close() error {
	s.cancel()
	s.options.close()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

type Client struct {
	ctx         context.Context
	cancel      context.CancelFunc
	options     *streamOptions
	mu          sync.Mutex
	connections map[*trackedConn]struct{}
}

var _ adapter.V2RayClientTransport = (*Client)(nil)

func NewClient(ctx context.Context, nativeDialer N.Dialer, _ M.Socksaddr, options Config, tlsConfig tls.Config) (*Client, error) {
	if tlsConfig != nil {
		return nil, fmt.Errorf("XDrive uses HTTPS storage and VLESS encryption; tunnel TLS must be disabled")
	}
	if err := validate(options); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Client{ctx: ctx, cancel: cancel, options: &streamOptions{ProtocolSettings: &options, ctx: ctx, dialer: nativeDialer}}, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	// A request can be cancelled independently of the transport, including while
	// the storage announce request waits for a response.
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	defer cancel()
	conn, err := dialSession(ctx, c.options)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if err = c.ctx.Err(); err != nil {
		c.mu.Unlock()
		conn.Close()
		return nil, err
	}
	tracked := &trackedConn{Conn: conn, owner: c}
	if c.connections == nil {
		c.connections = map[*trackedConn]struct{}{}
	}
	c.connections[tracked] = struct{}{}
	c.mu.Unlock()
	return tracked, nil
}

func (c *Client) Close() error {
	c.cancel()
	c.mu.Lock()
	connections := make([]*trackedConn, 0, len(c.connections))
	for conn := range c.connections {
		connections = append(connections, conn)
	}
	c.mu.Unlock()
	for _, conn := range connections {
		conn.Close()
	}
	c.options.close()
	return nil
}

type trackedConn struct {
	net.Conn
	owner *Client
	once  sync.Once
	err   error
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.err = c.Conn.Close(); c.owner.mu.Lock(); delete(c.owner.connections, c); c.owner.mu.Unlock() })
	return c.err
}
