package masque

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/masque"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.MASQUEOutboundOptions](registry, C.TypeMASQUE, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	ctx          context.Context
	dnsRouter    adapter.DNSRouter
	logger       logger.ContextLogger
	options      option.MASQUEOutboundOptions
	tunnel       *masque.Tunnel
	startHandler func()

	await      chan struct{}
	warpConfig *Config
	peer       M.Socksaddr
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.MASQUEOutboundOptions) (adapter.Outbound, error) {
	outbound := &Outbound{
		Adapter:   outbound.NewAdapterWithDialerOptions(C.TypeMASQUE, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, options.DialerOptions),
		ctx:       ctx,
		dnsRouter: service.FromContext[adapter.DNSRouter](ctx),
		logger:    logger,
		options:   options,
		await:     make(chan struct{}),
	}
	if options.Device != nil {
		config, err := deviceConfig(options.Device)
		if err != nil {
			return nil, err
		}
		outbound.warpConfig = config
	}
	outbound.startHandler = func() {
		defer close(outbound.await)
		cacheFile := service.FromContext[adapter.CacheFile](ctx)
		appConfig := outbound.warpConfig
		var err error
		if appConfig == nil && !options.Profile.Recreate && cacheFile != nil && cacheFile.StoreMASQUEConfig() {
			savedProfile := cacheFile.LoadBinary(tag)
			if savedProfile != nil {
				if err = json.Unmarshal(savedProfile.Content, &appConfig); err != nil {
					logger.ErrorContext(ctx, err)
					return
				}
			}
		}
		if appConfig == nil {
			appConfig, err = outbound.createConfig()
			if err != nil {
				logger.ErrorContext(ctx, err)
				return
			}
			if cacheFile != nil && cacheFile.StoreMASQUEConfig() {
				content, err := json.Marshal(appConfig)
				if err != nil {
					logger.ErrorContext(ctx, err)
					return
				}
				cacheFile.SaveBinary(tag, &adapter.SavedBinary{
					LastUpdated: time.Now(),
					Content:     content,
					LastEtag:    "",
				})
			}
		}
		outbound.warpConfig = appConfig
		privKey, err := appConfig.GetEcPrivateKey()
		if err != nil {
			logger.ErrorContext(ctx, E.New("failed to get private key: ", err))
			return
		}
		peerPubKey, err := appConfig.GetEcEndpointPublicKey()
		if err != nil {
			logger.ErrorContext(ctx, E.New("failed to get public key: ", err))
			return
		}
		cert, err := masque.GenerateCert(privKey, &privKey.PublicKey)
		if err != nil {
			logger.ErrorContext(ctx, E.New("failed to generate cert: ", err))
			return
		}
		tlsConfig, err := tls.NewMASQUEClient(ctx, logger, "consumer-masque.cloudflareclient.com", cert, privKey, peerPubKey, options.MASQUEOutboundTLSOptions)
		if err != nil {
			logger.ErrorContext(ctx, E.New("failed to prepare TLS config: ", err))
			return
		}
		endpoint, err := appConfig.SelectEndpointFromConfig(options.UseHTTP2, options.UseIPv6, 443)
		if err == nil && outbound.peer.Addr.IsValid() {
			if options.UseHTTP2 {
				endpoint = net.TCPAddrFromAddrPort(netip.AddrPortFrom(outbound.peer.Addr, outbound.peer.Port))
			} else {
				endpoint = net.UDPAddrFromAddrPort(netip.AddrPortFrom(outbound.peer.Addr, outbound.peer.Port))
			}
		}
		if err != nil {
			logger.ErrorContext(ctx, E.New("failed to select endpoint: ", err))
			return
		}
		var udpTimeout time.Duration
		if options.UDPTimeout != 0 {
			udpTimeout = time.Duration(options.UDPTimeout)
		} else {
			udpTimeout = C.UDPTimeout
		}
		var udpKeepalivePeriod time.Duration
		if options.UDPKeepalivePeriod != 0 {
			udpKeepalivePeriod = time.Duration(options.UDPKeepalivePeriod)
		} else {
			udpKeepalivePeriod = time.Second * 30
		}
		outboundDialer, err := dialer.NewWithOptions(dialer.Options{
			Context:          ctx,
			Options:          options.DialerOptions,
			RemoteIsDomain:   false,
			ResolverOnDetour: true,
		})
		if err != nil {
			logger.ErrorContext(ctx, err)
			return
		}
		addresses, err := appConfig.Addresses()
		if err != nil {
			logger.ErrorContext(ctx, err)
			return
		}
		tunnel, err := masque.NewTunnel(
			ctx,
			logger,
			masque.TunnelOptions{
				Dialer:               outboundDialer,
				Address:              addresses,
				Endpoint:             endpoint,
				TLSConfig:            tlsConfig,
				UseHTTP2:             options.UseHTTP2,
				UDPTimeout:           udpTimeout,
				UDPKeepalivePeriod:   udpKeepalivePeriod,
				UDPInitialPacketSize: options.UDPInitialPacketSize,
				ReconnectDelay:       options.ReconnectDelay.Build(),
			})
		if err != nil {
			logger.ErrorContext(ctx, err)
			return
		}
		outbound.tunnel = tunnel
		if err = outbound.tunnel.Start(false); err != nil {
			logger.ErrorContext(ctx, err)
			return
		}
		if err = outbound.tunnel.Start(true); err != nil {
			logger.ErrorContext(ctx, err)
			return
		}
	}
	return outbound, nil
}

func (w *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage == adapter.StartStateInitialize {
		scope.Add(w.Close)
	}
	if stage != adapter.StartStatePostStart {
		return nil
	}
	go w.startHandler()
	return nil
}

func (w *Outbound) Close() error {
	select {
	case <-w.await:
		if w.tunnel != nil {
			return w.tunnel.Close()
		}
	default:
	}
	return nil
}

func (w *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if err := w.isTunnelInitialized(ctx); err != nil {
		return nil, err
	}
	switch network {
	case N.NetworkTCP:
		w.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		w.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	if destination.IsDomain() {
		destinationAddresses, err := w.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, err
		}
		return N.DialSerial(ctx, w.tunnel, network, destination, destinationAddresses)
	} else if !destination.Addr.IsValid() {
		return nil, E.New("invalid destination: ", destination)
	}
	return w.tunnel.DialContext(ctx, network, destination)
}

func (w *Outbound) ListenPacketWithDestination(ctx context.Context, destination M.Socksaddr) (net.PacketConn, netip.Addr, error) {
	if err := w.isTunnelInitialized(ctx); err != nil {
		return nil, netip.Addr{}, err
	}
	w.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	if destination.IsDomain() {
		destinationAddresses, err := w.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, netip.Addr{}, err
		}
		return N.ListenSerial(ctx, w.tunnel, destination, destinationAddresses)
	}
	packetConn, err := w.tunnel.ListenPacket(ctx, destination)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	if destination.IsIP() {
		return packetConn, destination.Addr, nil
	}
	return packetConn, netip.Addr{}, nil
}

func (w *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	packetConn, destinationAddress, err := w.ListenPacketWithDestination(ctx, destination)
	if err != nil {
		return nil, err
	}
	if destinationAddress.IsValid() && destination != M.SocksaddrFrom(destinationAddress, destination.Port) {
		return bufio.NewNATPacketConn(bufio.NewPacketConn(packetConn), M.SocksaddrFrom(destinationAddress, destination.Port), destination), nil
	}
	return packetConn, nil
}

func (w *Outbound) isTunnelInitialized(ctx context.Context) error {
	select {
	case <-w.await:
	case <-ctx.Done():
		return ctx.Err()
	}
	if w.tunnel == nil {
		return E.New("tunnel not initialized")
	}
	return nil
}

// newWARPOutbound accepts an already enrolled MASQUE device without an API request.
func newWARPOutbound(ctx context.Context, logger log.ContextLogger, tag string, options option.MASQUEOutboundOptions, config *Config, peer M.Socksaddr, _ string) (*Outbound, error) {
	created, err := NewOutbound(ctx, nil, logger, tag, options)
	if err != nil {
		return nil, err
	}
	out := created.(*Outbound)
	if config != nil {
		out.warpConfig = config
	}
	out.peer = peer
	return out, nil
}

// NewWARPOutbound exposes a WARP device as a lifecycle-managed MASQUE endpoint.
func NewWARPOutbound(ctx context.Context, logger log.ContextLogger, tag string, options option.MASQUEOutboundOptions, config *Config, peer M.Socksaddr, cacheKey string) (*Outbound, error) {
	return newWARPOutbound(ctx, logger, tag, options, config, peer, cacheKey)
}
