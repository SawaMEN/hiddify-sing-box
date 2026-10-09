package fptn

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	wire "github.com/sagernet/sing-box/transport/fptn"
	"github.com/sagernet/sing-box/transport/iptunnel"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func RegisterOutbound(r *outbound.Registry) { outbound.Register(r, "fptn", NewOutbound) }

type session struct {
	ws      *websocket.Conn
	network *iptunnel.Network
	writeMu sync.Mutex
	once    sync.Once
}

func (s *session) close() { s.once.Do(func() { s.ws.Close(); s.network.Close() }) }

type Outbound struct {
	outbound.Adapter
	ctx     context.Context
	cancel  context.CancelFunc
	opts    option.FPTNOutboundOptions
	dialer  N.Dialer
	dns     adapter.DNSRouter
	mu      sync.Mutex
	current *session
	closed  bool
}

func NewOutbound(ctx context.Context, _ adapter.Router, _ log.ContextLogger, tag string, o option.FPTNOutboundOptions) (adapter.Outbound, error) {
	fp, e := hex.DecodeString(o.Fingerprint)
	if e != nil || len(fp) != 16 {
		return nil, fmt.Errorf("fptn: MD5 certificate pin is required")
	}
	o.Fingerprint = strings.ToLower(o.Fingerprint)
	if o.Username == "" || o.Password == "" || o.Server == "" || o.ServerPort == 0 {
		return nil, fmt.Errorf("fptn: endpoint and credentials are required")
	}
	if o.Bypass == "" {
		o.Bypass = "obfuscation"
	}
	if o.Bypass != "obfuscation" && o.Bypass != "sni-spoofing" {
		return nil, fmt.Errorf("fptn: unsupported bypass mode")
	}
	if o.SNI == "" {
		o.SNI = "www.google.com"
	}
	if strings.ContainsAny(o.SNI, "\r\n\x00") {
		return nil, fmt.Errorf("fptn: invalid SNI")
	}
	if o.MTU == 0 {
		o.MTU = 1500
	}
	if o.MTU < 1280 || o.MTU > 9000 {
		return nil, fmt.Errorf("fptn: MTU must be 1280–9000")
	}
	d, e := dialer.New(ctx, o.DialerOptions, o.ServerIsDomain())
	if e != nil {
		return nil, e
	}
	life, cancel := context.WithCancel(ctx)
	return &Outbound{Adapter: outbound.NewAdapterWithDialerOptions("fptn", tag, []string{N.NetworkTCP, N.NetworkUDP}, o.DialerOptions), ctx: life, cancel: cancel, opts: o, dialer: d, dns: service.FromContext[adapter.DNSRouter](ctx)}, nil
}
func (h *Outbound) dialTLS(ctx context.Context, n, a string) (net.Conn, error) {
	raw, e := h.dialer.DialContext(ctx, N.NetworkTCP, h.opts.ServerOptions.Build())
	if e != nil {
		return nil, e
	}
	return wire.DialTLS(ctx, raw, h.opts.SNI, h.opts.Fingerprint, h.opts.Bypass)
}
func (h *Outbound) ensure(ctx context.Context) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, net.ErrClosed
	}
	if h.current != nil {
		return h.current, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	host := h.opts.ServerOptions.Build().String()
	tr := &http.Transport{DialTLSContext: h.dialTLS, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	body, _ := json.Marshal(map[string]string{"username": h.opts.Username, "password": h.opts.Password})
	req, _ := http.NewRequestWithContext(ctx, "POST", (&url.URL{Scheme: "https", Host: host, Path: "/api/v1/login"}).String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, e := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if e != nil {
		return nil, fmt.Errorf("fptn: authentication connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fptn: authentication failed (HTTP %d)", resp.StatusCode)
	}
	var login struct {
		Token string `json:"access_token"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&login); e != nil || login.Token == "" || strings.ContainsAny(login.Token, "\r\n") {
		return nil, fmt.Errorf("fptn: invalid authentication response")
	}
	wd := websocket.Dialer{NetDialTLSContext: h.dialTLS, HandshakeTimeout: 20 * time.Second}
	headers := http.Header{"Authorization": []string{"Bearer " + login.Token}, "Client-Agent": []string{"Hiddify/FPTN-Go"}}
	ws, r, e := wd.DialContext(ctx, (&url.URL{Scheme: "wss", Host: host, Path: "/api/v1/fptn"}).String(), headers)
	if e != nil {
		if r != nil {
			r.Body.Close()
		}
		return nil, fmt.Errorf("fptn: WebSocket connection failed")
	}
	ws.SetReadLimit(256 * 1024)
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	stopRead := context.AfterFunc(ctx, func() { ws.Close() })
	kind, b, e := ws.ReadMessage()
	stopRead()
	if e != nil || kind != websocket.BinaryMessage {
		ws.Close()
		return nil, fmt.Errorf("fptn: IP assignment failed")
	}
	addresses, e := wire.DecodeAssignment(b)
	if e != nil {
		ws.Close()
		return nil, e
	}
	ws.SetReadDeadline(time.Time{})
	s := &session{ws: ws}
	s.network, e = iptunnel.New(addresses, h.opts.MTU, func(b []byte) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		s.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		e := s.ws.WriteMessage(websocket.BinaryMessage, wire.EncodePacket(b))
		if e != nil {
			s.ws.Close()
		}
		return e
	})
	if e != nil {
		ws.Close()
		return nil, e
	}
	s.network.Resolve = h.resolve
	h.current = s
	go h.read(s)
	return s, nil
}
func (h *Outbound) read(s *session) {
	defer func() {
		s.close()
		h.mu.Lock()
		if h.current == s {
			h.current = nil
		}
		h.mu.Unlock()
	}()
	for {
		kind, b, e := s.ws.ReadMessage()
		if e != nil {
			return
		}
		if kind != websocket.BinaryMessage {
			return
		}
		packets, e := wire.DecodePackets(b)
		if e != nil {
			return
		}
		for _, p := range packets {
			if s.network.Inject(p) != nil {
				return
			}
		}
	}
}
func (h *Outbound) resolve(ctx context.Context, d M.Socksaddr) (M.Socksaddr, error) {
	if !d.IsDomain() {
		return d, nil
	}
	if h.dns == nil {
		return M.Socksaddr{}, fmt.Errorf("fptn: DNS router unavailable")
	}
	a, e := h.dns.Lookup(ctx, d.Fqdn, adapter.DNSQueryOptions{})
	if e != nil {
		return M.Socksaddr{}, e
	}
	if len(a) == 0 {
		return M.Socksaddr{}, fmt.Errorf("fptn: empty DNS response")
	}
	return M.SocksaddrFrom(a[0], d.Port), nil
}
func (h *Outbound) DialContext(ctx context.Context, n string, d M.Socksaddr) (net.Conn, error) {
	s, e := h.ensure(ctx)
	if e != nil {
		return nil, e
	}
	d, e = h.resolve(ctx, d)
	if e != nil {
		return nil, e
	}
	return s.network.DialContext(ctx, n, d)
}
func (h *Outbound) ListenPacket(ctx context.Context, d M.Socksaddr) (net.PacketConn, error) {
	s, e := h.ensure(ctx)
	if e != nil {
		return nil, e
	}
	return s.network.ListenPacket(ctx, d)
}
func (h *Outbound) Close() error {
	h.cancel()
	h.mu.Lock()
	h.closed = true
	s := h.current
	h.current = nil
	h.mu.Unlock()
	if s != nil {
		s.close()
	}
	return nil
}
