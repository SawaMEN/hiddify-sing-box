package dnstt

import (
	"context"
	"fmt"
	"math/rand"
	"net"

	"github.com/sagernet/sing-box/common/monitoring"

	dnstt "github.com/net2share/vaydns/client"
)

func (receiver *Outbound) addResolver(resolver dnstt.Resolver) {
	receiver.mu.Lock()
	if receiver.mdMgr != nil {
		// Smart pool mode: register the resolver as a multidns upstream.
		// c.resolvers stays at the single virtual resolver set up in
		// NewOutbound, so the existing tunnel keeps running — the pool
		// rotates upstreams behind it. No need to invalidate
		// c.mutlitunnel here (unlike the legacy path, which has to
		// rebuild the tunnel when its resolver list changes).
		_, _ = receiver.mdMgr.AddResolverURL(resolverURL(resolver))
	} else {
		// for i := 0; i < c.options.TunnelPerResolver; i++ {
		receiver.resolvers = append(receiver.resolvers, resolver)
		receiver.tunnels = append(receiver.tunnels, nil)
		// }
		receiver.mutlitunnel = nil
	}
	receiver.mu.Unlock()
	if !receiver.IsReady() {
		receiver.started = 1
		receiver.logger.InfoContext(receiver.ctx, "initial resolver ", resolver.ResolverAddr)
		monitoring.Get(receiver.ctx).TestNow(receiver.Tag())
	}
}

// resolverURL renders a dnstt.Resolver as the URL form multidns parses.
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

func (receiver *Outbound) openStreamImp(ctx context.Context) (net.Conn, error) {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	if t := receiver.mutlitunnel; t != nil {
		return t.OpenStream()
	}

	if newtunnel, err := receiver.createDnsttTunnel(ctx, receiver.resolvers); err != nil {
		return nil, err
	} else {
		receiver.mutlitunnel = newtunnel
		return newtunnel.OpenStream()
	}
}

func (receiver *Outbound) OpenStream(ctx context.Context) (net.Conn, error) {
	var err error
	var conn net.Conn
	for range 3 {
		conn, err = receiver.openStreamImp(ctx)
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}

func (receiver *Outbound) OpenStreamSingleResolver(ctx context.Context) (net.Conn, error) {
	// dnsttConfig := streamSettings.ProtocolSettings.(*Config)
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	// c.tunnel_index = (c.tunnel_index + 1) % len(c.tunnels)
	var lasterr error
	for i := 0; i < max(5, len(receiver.tunnels)); i++ {
		tunnel_index := rand.Intn(len(receiver.resolvers)) // 0 <= x < n
		tunnel := receiver.tunnels[tunnel_index]
		if tunnel == nil {
			if newtunnel, err := receiver.createDnsttTunnel(ctx, []dnstt.Resolver{receiver.resolvers[tunnel_index]}); err != nil {
				lasterr = err
				receiver.logger.DebugContext(ctx, "tunnel [", tunnel_index, "]  failed resolver ", receiver.resolvers[tunnel_index].ResolverAddr)
			} else {
				receiver.tunnels[tunnel_index] = newtunnel
				tunnel = newtunnel
				receiver.logger.InfoContext(ctx, "tunnel [", tunnel_index, "] resolver ", receiver.resolvers[tunnel_index].ResolverAddr)

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
			receiver.tunnels[tunnel_index] = nil
		}
	}
	return nil, lasterr
}

func (receiver *Outbound) createDnsttTunnel(ctx context.Context, resolver []dnstt.Resolver) (*dnstt.Tunnel, error) {
	tServer, err := dnstt.NewTunnelServer(receiver.options.Domain, receiver.options.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel server: %w", err)
	}

	if receiver.options.MTU != nil {
		tServer.MTU = *receiver.options.MTU
	}

	tServer.DnsttCompat = receiver.options.DnsttCompat

	if receiver.options.ClientIDSize != nil {
		tServer.ClientIDSize = *receiver.options.ClientIDSize
	}

	if receiver.options.MaxNumLabels != nil {
		tServer.MaxNumLabels = *receiver.options.MaxNumLabels
	}
	if receiver.options.RPS != nil {
		tServer.RPS = *receiver.options.RPS
	}
	if receiver.options.MaxQnameLen != nil {
		tServer.MaxQnameLen = *receiver.options.MaxQnameLen
	}

	tunnel, err := dnstt.NewTunnel(resolver, tServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create tunnel: %w", err)
	}
	if receiver.options.OpenStreamTimeout != nil {
		tunnel.OpenStreamTimeout = receiver.options.OpenStreamTimeout.Build()
	}
	if receiver.options.MaxStreams != nil {
		tunnel.MaxStreams = *receiver.options.MaxStreams
	}
	if receiver.options.ReconnectMinDelay != nil {
		tunnel.ReconnectMinDelay = receiver.options.ReconnectMinDelay.Build()
	}
	if receiver.options.ReconnectMaxDelay != nil {
		tunnel.ReconnectMaxDelay = receiver.options.ReconnectMaxDelay.Build()
	}
	if receiver.options.SessionCheckInterval != nil {
		tunnel.SessionCheckInterval = receiver.options.SessionCheckInterval.Build()
	}
	if receiver.options.IdleTimeout != nil {
		tunnel.IdleTimeout = receiver.options.IdleTimeout.Build()
	}
	if receiver.options.KeepAlive != nil {
		tunnel.KeepAlive = receiver.options.KeepAlive.Build()
	}
	if receiver.options.HandshakeTimeout != nil {
		tunnel.HandshakeTimeout = receiver.options.HandshakeTimeout.Build()
	}

	if err := tunnel.InitiateResolverConnection(); err != nil {
		return nil, fmt.Errorf("failed to initiate connection to resolver: %w", err)
	}

	if err := tunnel.InitiateDNSPacketConn(tServer.Addr); err != nil {
		return nil, fmt.Errorf("failed to initiate DNS packet connection: %w", err)
	}

	// c.logger.DebugContext(c.ctx, "effective MTU %d", tServer.MTU)

	if err := tunnel.InitiateKCPConn(tServer.MTU); err != nil {
		return nil, fmt.Errorf("failed to initiate KCP connection: %w", err)
	}

	// c.logger.DebugContext(c.ctx, "established KCP conn")
	if err := tunnel.InitiateNoiseChannel(); err != nil {
		// c.logger.WarnContext(c.ctx, "failed to establish Noise channel: %v", err)
		return nil, fmt.Errorf("failed to initiate noise channel: %w", err)
	}

	// c.logger.DebugContext(c.ctx, "established Noise channel")

	if err := tunnel.InitiateSmuxSession(); err != nil {
		return nil, fmt.Errorf("failed to initiate smux session: %w", err)
	}
	// c.logger.DebugContext(c.ctx, "established smux session")
	return tunnel, nil
}
