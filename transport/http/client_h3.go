//go:build with_quic

package http

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/common/httpclient"
	qtls "github.com/sagernet/sing-quic"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
)

func init() {
	NewHTTP3Client = newHTTP3Client
}

type http3ClientImpl struct {
	dialer        N.Dialer
	tlsConfig     aTLS.Config
	server        M.Socksaddr
	authority     string
	headers       http.Header
	authorization string
	quicConfig    *quic.Config
	transport     *http3.Transport
	access        sync.Mutex
	conn          *http3.ClientConn
	rawConn       net.Conn
}

func newHTTP3Client(options ClientOptions, authorization string) (http3Client, error) {
	if options.TLSConfig == nil {
		return nil, E.New("HTTP/3 requires TLS")
	}
	dialer := options.RawDialer
	if dialer == nil {
		dialer = N.SystemDialer
	}
	quicConfig := httpclient.NewQUICConfig(options.HTTP3Options)
	quicConfig.EnableDatagrams = true
	headers := options.Headers.Clone()
	authority := options.Server.String()
	if options.Authority != "" {
		authority = options.Authority
	}
	if headers != nil {
		if host := headers.Get("Host"); host != "" {
			authority = host
		}
		headers.Del("Host")
	}
	return &http3ClientImpl{
		dialer:        dialer,
		tlsConfig:     options.TLSConfig,
		server:        options.Server,
		authority:     authority,
		headers:       headers,
		authorization: authorization,
		quicConfig:    quicConfig,
		transport:     &http3.Transport{EnableDatagrams: true, DisableCompression: true},
	}, nil
}

func (c *http3ClientImpl) acquire(ctx context.Context) (*http3.ClientConn, error) {
	c.access.Lock()
	defer c.access.Unlock()
	if c.conn != nil && c.conn.Context().Err() == nil {
		return c.conn, nil
	}
	if c.rawConn != nil {
		c.rawConn.Close()
		c.rawConn = nil
	}
	rawConn, err := c.dialer.DialContext(ctx, N.NetworkUDP, c.server)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, E.Cause1(ErrHTTP3Unavailable, err)
	}
	quicConn, err := qtls.DialEarly(ctx, rawConn, c.tlsConfig, c.quicConfig)
	if err != nil {
		rawConn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, wrapHTTP3Error(err) // H
	}
	c.conn = c.transport.NewClientConn(quicConn)
	c.rawConn = rawConn
	return c.conn, nil
}

func (c *http3ClientImpl) openStream(ctx context.Context, request *http.Request) (*http3.RequestStream, *http3.ClientConn, error) {
	clientConn, err := c.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	stream, err := clientConn.OpenRequestStream(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, wrapHTTP3Error(err) // H
	}
	stop := context.AfterFunc(ctx, func() {
		stream.CancelRead(0)
		stream.CancelWrite(0)
	})
	var response *http.Response
	err = stream.SendRequestHeader(request)
	if err == nil {
		response, err = stream.ReadResponse()
	}
	if err == nil {
		select {
		case <-clientConn.ReceivedSettings():
		case <-clientConn.Context().Done():
			err = context.Cause(clientConn.Context())
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if !stop() {
		err = ctx.Err()
	}
	if err != nil {
		stream.CancelRead(0)
		stream.Close()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, E.Cause(wrapHTTP3Error(err), "HTTP/3 CONNECT")
	}
	if response.StatusCode != http.StatusOK {
		stream.CancelRead(0)
		stream.Close()
		return nil, nil, statusError(response)
	}
	return stream, clientConn, nil
}

func (c *http3ClientImpl) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	stream, _, err := c.openStream(ctx, &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: destination.String()},
		Host:   destination.String(),
		Header: buildRequestHeader(c.headers, c.authorization, false),
	})
	if err != nil {
		return nil, err
	}
	return &http3StreamConn{stream: stream, remoteAddr: destination}, nil
}

func (c *http3ClientImpl) OpenTunnel(ctx context.Context, request tunnelRequest) (DatagramStream, error) {
	requestURL := *request.url
	requestURL.Scheme = "https"
	requestURL.Host = c.authority
	header := buildRequestHeader(c.headers, c.authorization, request.originAuthorization)
	header.Set("Capsule-Protocol", "?1")
	stream, clientConn, err := c.openStream(ctx, &http.Request{
		Method: http.MethodConnect,
		Proto:  request.protocol,
		URL:    &requestURL,
		Host:   c.authority,
		Header: header,
	})
	if err != nil {
		return nil, err
	}
	return &http3RequestDatagramStream{stream: stream, datagramsEnabled: clientConn.Settings().EnableDatagrams}, nil
}

func (c *http3ClientImpl) ResetConnection() {
	c.access.Lock()
	defer c.access.Unlock()
	if c.conn != nil {
		c.conn.CloseWithError(0, "")
		c.conn = nil
	}
	if c.rawConn != nil {
		c.rawConn.Close()
		c.rawConn = nil
	}
}

func (c *http3ClientImpl) Close() error {
	c.access.Lock()
	defer c.access.Unlock()
	if c.conn != nil {
		c.conn.CloseWithError(0, "")
		c.conn = nil
	}
	if c.rawConn != nil {
		c.rawConn.Close()
		c.rawConn = nil
	}
	return c.transport.Close()
}

type http3StreamConn struct {
	stream      *http3.RequestStream
	remoteAddr  net.Addr
	writeAccess sync.Mutex
	closed      atomic.Bool
}

func (c *http3StreamConn) Read(p []byte) (int, error) {
	n, err := c.stream.Read(p)
	return n, c.wrapError(err)
}

func (c *http3StreamConn) Write(p []byte) (int, error) {
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	n, err := c.stream.Write(p)
	return n, c.wrapError(err)
}

func (c *http3StreamConn) wrapError(err error) error {
	if err == nil {
		return nil
	}
	if c.closed.Load() {
		return net.ErrClosed
	}
	return wrapHTTP3Error(err)
}

func (c *http3StreamConn) CloseWrite() error {
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	return c.stream.Close()
}

func (c *http3StreamConn) Close() error {
	c.closed.Store(true)
	c.stream.SetWriteDeadline(time.Now())
	c.writeAccess.Lock()
	defer c.writeAccess.Unlock()
	c.stream.CancelRead(0)
	return c.stream.Close()
}

func (c *http3StreamConn) LocalAddr() net.Addr {
	return M.Socksaddr{}
}

func (c *http3StreamConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *http3StreamConn) SetDeadline(t time.Time) error {
	return c.stream.SetDeadline(t)
}

func (c *http3StreamConn) SetReadDeadline(t time.Time) error {
	return c.stream.SetReadDeadline(t)
}

func (c *http3StreamConn) SetWriteDeadline(t time.Time) error {
	return c.stream.SetWriteDeadline(t)
}

func (c *http3StreamConn) NeedAdditionalReadDeadline() bool {
	return true
}

type http3RequestDatagramStream struct {
	stream           *http3.RequestStream
	datagramsEnabled bool
	closed           atomic.Bool
}

func (s *http3RequestDatagramStream) Read(p []byte) (int, error) {
	n, err := s.stream.Read(p)
	return n, s.wrapError(err)
}

func (s *http3RequestDatagramStream) Write(p []byte) (int, error) {
	n, err := s.stream.Write(p)
	return n, s.wrapError(err)
}

func (s *http3RequestDatagramStream) Close() error {
	s.closed.Store(true)
	s.stream.SetWriteDeadline(time.Now())
	s.stream.CancelRead(0)
	return s.stream.Close()
}

func (s *http3RequestDatagramStream) SendDatagram(payload []byte) error {
	if !s.datagramsEnabled {
		return ErrDatagramUnsupported
	}
	err := s.stream.SendDatagram(payload)
	if err == nil {
		return nil
	}
	if tooLarge, ok := errors.AsType[*quic.DatagramTooLargeError](err); ok {
		return &DatagramTooLargeError{MaxPayloadSize: int(tooLarge.MaxDatagramPayloadSize) - VarintLen(uint64(s.stream.StreamID()/4))}
	}
	return s.wrapError(err)
}

func (s *http3RequestDatagramStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	data, err := s.stream.ReceiveDatagram(ctx)
	return data, s.wrapError(err)
}

var (
	_ net.Conn       = (*http3StreamConn)(nil)
	_ N.WriteCloser  = (*http3StreamConn)(nil)
	_ DatagramStream = (*http3RequestDatagramStream)(nil)
)

// wrapHTTP3Error marks connection-level QUIC failures as eligible for HTTP/2
// fallback, while preserving authentication, certificate and caller errors.
func wrapHTTP3Error(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return err
	}
	if transportErr, ok := errors.AsType[*quic.TransportError](err); ok {
		if transportErr.ErrorCode == 0x178 { // QUIC NO_APPLICATION_PROTOCOL
			return E.Cause1(ErrHTTP3Unavailable, err)
		}
		return err
	}
	if h3Err, ok := errors.AsType[*http3.Error](err); ok {
		if h3Err.Remote {
			return E.Cause1(ErrHTTP3Unavailable, err)
		}
		return err
	}
	if appErr, ok := errors.AsType[*quic.ApplicationError](err); ok {
		if appErr.Remote {
			return E.Cause1(ErrHTTP3Unavailable, err)
		}
		return err
	}
	var handshakeErr *quic.HandshakeTimeoutError
	var idleErr *quic.IdleTimeoutError
	var resetErr *quic.StatelessResetError
	if errors.As(err, &handshakeErr) || errors.As(err, &idleErr) ||
		errors.As(err, &resetErr) || errors.Is(err, io.EOF) {
		return E.Cause1(ErrHTTP3Unavailable, err)
	}
	return qtls.WrapError(err)
}

func (s *http3RequestDatagramStream) wrapError(err error) error {
	if err == nil {
		return nil
	}
	if s.closed.Load() {
		return net.ErrClosed
	}
	return wrapHTTP3Error(err)
}
