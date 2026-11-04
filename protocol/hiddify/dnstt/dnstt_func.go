package dnstt

import (
	"context"
	"github.com/sagernet/sing-box/option"
	"math/rand"
	"net"

	"fmt"

	dnstt "github.com/net2share/vaydns/client"
	"github.com/sagernet/sing-box/common/monitoring"
)

func (h *Outbound) addResolver(resolver dnstt.Resolver) {
	h.mu.Lock()
	if h.pool != nil {
		// Smart pool mode: register the resolver as a pool upstream.
		// h.resolvers stays at the single virtual resolver set up in
		// NewOutbound, so the existing tunnel keeps running — the pool
		// rotates upstreams behind it. No need to invalidate
		// h.mutlitunnel here (unlike the legacy path, which has to
		// rebuild the tunnel when its resolver list changes).
		_ = h.pool.Add(resolverURL(resolver))
	} else {
		// for i := 0; i < h.options.TunnelPerResolver; i++ {
		h.resolvers = append(h.resolvers, resolver)
		h.tunnels = append(h.tunnels, nil)
		// }
		h.mutlitunnel = nil
	}
	h.mu.Unlock()
	if !h.isReady() {
		h.started = 1
		h.logger.InfoContext(h.ctx, "initial resolver ", resolver.ResolverAddr)
		monitoring.Get(h.ctx).TestNow(h.Tag())
	}
}

// resolverURL renders a dnstt.Resolver as the URL form the pool parses.
// UDP resolvers are bare host:port (multidns defaults to UDP/53); DoT gets
// the "dot://" prefix; DoH is already a full https:// URL.
func resolverURL(r dnstt.Resolver) string {
	switch r.ResolverType {
	case dnstt.ResolverTypeDOT:
		return "dot://" + r.ResolverAddr
	default:
		return r.ResolverAddr
	}
}

func (h *Outbound) openStreamImp(ctx context.Context) (net.Conn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.mutlitunnel; t != nil {
		return t.OpenStream()
	}

	if newtunnel, err := h.createDnsttTunnel(ctx, h.resolvers); err != nil {
		return nil, err
	} else {
		h.mutlitunnel = newtunnel
		return newtunnel.OpenStream()
	}
}
func (h *Outbound) OpenStream(ctx context.Context) (net.Conn, error) {
	var err error
	var conn net.Conn
	for range 3 {
		conn, err = h.openStreamImp(ctx)
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}
func (h *Outbound) OpenStreamSingleResolver(ctx context.Context) (net.Conn, error) {
	// dnsttConfig := streamSettings.ProtocolSettings.(*Config)
	h.mu.Lock()
	defer h.mu.Unlock()
	// h.tunnel_index = (h.tunnel_index + 1) % len(h.tunnels)
	var lasterr error
	for i := 0; i < max(5, len(h.tunnels)); i++ {
		tunnel_index := rand.Intn(len(h.resolvers)) // 0 <= x < n
		tunnel := h.tunnels[tunnel_index]
		if tunnel == nil {
			if newtunnel, err := h.createDnsttTunnel(ctx, []dnstt.Resolver{h.resolvers[tunnel_index]}); err != nil {
				lasterr = err
				h.logger.DebugContext(ctx, "tunnel [", tunnel_index, "]  failed resolver ", h.resolvers[tunnel_index].ResolverAddr)
			} else {
				h.tunnels[tunnel_index] = newtunnel
				tunnel = newtunnel
				h.logger.InfoContext(ctx, "tunnel [", tunnel_index, "] resolver ", h.resolvers[tunnel_index].ResolverAddr)

				// return tunnel, nil
			}
		}
		if tunnel != nil {
			if conn, err := tunnel.OpenStream(); err == nil {
				// return &LoggingConn{Conn: stream, outbound: h, tunnel_index: h.tunnel_index}, nil
				return conn, nil
			} else {
				lasterr = err
			}
			tunnel.Close()
			h.tunnels[tunnel_index] = nil
		}
	}
	return nil, lasterr
}
func (h *Outbound) createDnsttTunnel(ctx context.Context, resolver []dnstt.Resolver) (*dnstt.Tunnel, error) {
	tServer, err := newTunnelServer(h.options)
	if err != nil {
		return nil, err
	}

	tunnel, err := dnstt.NewTunnel(resolver, tServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	if h.options.OpenStreamTimeout != nil {
		tunnel.OpenStreamTimeout = h.options.OpenStreamTimeout.Build()
	}
	if h.options.MaxStreams != nil {
		tunnel.MaxStreams = *h.options.MaxStreams
	}
	if h.options.ReconnectMinDelay != nil {
		tunnel.ReconnectMinDelay = h.options.ReconnectMinDelay.Build()
	}
	if h.options.ReconnectMaxDelay != nil {
		tunnel.ReconnectMaxDelay = h.options.ReconnectMaxDelay.Build()
	}
	if h.options.SessionCheckInterval != nil {
		tunnel.SessionCheckInterval = h.options.SessionCheckInterval.Build()
	}
	if h.options.IdleTimeout != nil {
		tunnel.IdleTimeout = h.options.IdleTimeout.Build()
	}
	if h.options.KeepAlive != nil {
		tunnel.KeepAlive = h.options.KeepAlive.Build()
	}
	if h.options.HandshakeTimeout != nil {
		tunnel.HandshakeTimeout = h.options.HandshakeTimeout.Build()
	}

	if err := tunnel.InitiateResolverConnection(); err != nil {
		return nil, fmt.Errorf("failed to initiate connection to resolver: %w", err)
	}

	if err := tunnel.InitiateDNSPacketConn(tServer.Addr); err != nil {
		return nil, fmt.Errorf("failed to initiate DNS packet connection: %w", err)
	}

	// h.logger.DebugContext(h.ctx, "effective MTU %d", tServer.MTU)

	if err := tunnel.InitiateKCPConn(tServer.MTU); err != nil {
		return nil, fmt.Errorf("failed to initiate KCP connection: %w", err)
	}

	// h.logger.DebugContext(h.ctx, "established KCP conn")
	if err := tunnel.InitiateNoiseChannel(); err != nil {
		// h.logger.WarnContext(h.ctx, "failed to establish Noise channel: %v", err)
		return nil, fmt.Errorf("failed to initiate noise channel: %w", err)
	}

	// h.logger.DebugContext(h.ctx, "established Noise channel")

	if err := tunnel.InitiateSmuxSession(); err != nil {
		return nil, fmt.Errorf("failed to initiate smux session: %w", err)
	}
	// h.logger.DebugContext(h.ctx, "established smux session")
	return tunnel, nil
}

// newTunnelServer builds the tunnel server settings from the options.
func newTunnelServer(options option.DnsttOptions) (dnstt.TunnelServer, error) {
	tServer, err := dnstt.NewTunnelServer(options.Domain, options.PublicKey)
	if err != nil {
		return tServer, fmt.Errorf("invalid tunnel server: %w", err)
	}
	if options.MTU != nil {
		tServer.MTU = *options.MTU
	}
	tServer.DnsttCompat = options.DnsttCompat
	if options.ClientIDSize != nil {
		tServer.ClientIDSize = *options.ClientIDSize
	}
	if options.MaxNumLabels != nil {
		tServer.MaxNumLabels = *options.MaxNumLabels
	}
	if options.RPS != nil {
		tServer.RPS = *options.RPS
	}
	if options.MaxQnameLen != nil {
		tServer.MaxQnameLen = *options.MaxQnameLen
	}
	tServer.RecordType = options.RecordType // was only used to name the saved resolver list
	return tServer, nil
}
