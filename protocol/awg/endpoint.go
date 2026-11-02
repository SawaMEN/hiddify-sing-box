package awg

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/monitoring"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/hiddify/peeruser"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/awg"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/format"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"go4.org/netipx"
)

func RegisterEndpoint(registry *endpoint.Registry) {
	endpoint.Register(registry, C.TypeAwg, NewEndpoint)
}

type Endpoint struct {
	*awg.Device
	endpoint.Adapter
	address   []netip.Prefix
	peerUsers *peeruser.Users // H
	router    adapter.Router
	logger    log.ContextLogger
	dnsRouter adapter.DNSRouter
	started   bool
	ctx       context.Context
}

func NewEndpoint(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.AwgEndpointOptions) (adapter.Endpoint, error) {
	if options.MTU == 0 {
		options.MTU = 1408
	}
	if err := applyMasquerade(&options.Awg); err != nil {
		return nil, err
	}
	fallback, err := peerFallback(options)
	if err != nil {
		return nil, err
	}
	reserved, err := reservedOptions(options)
	if err != nil {
		return nil, err
	}

	options.UDPFragmentDefault = true
	// Check if any peer has a domain address
	remoteIsDomain := common.Any(options.Peers, func(peer option.AwgPeerOptions) bool {
		return !M.ParseAddr(peer.Address).IsValid()
	})
	dial, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   remoteIsDomain,
		ResolverOnDetour: true,
		DirectOutbound:   true,
	})
	if err != nil {
		return nil, err
	}

	var allowedPrefixBuilder netipx.IPSetBuilder
	var excludedPrefixBuilder netipx.IPSetBuilder
	for _, peer := range options.Peers {
		for _, prefix := range peer.AllowedIPs {
			allowedPrefixBuilder.AddPrefix(prefix)
		}

		if addr, err := netip.ParseAddr(peer.Address); err == nil {
			excludedPrefixBuilder.Add(addr)
		}
	}
	allowedIps, err := allowedPrefixBuilder.IPSet()
	if err != nil {
		return nil, err
	}
	excludedIps, err := excludedPrefixBuilder.IPSet()
	if err != nil {
		return nil, err
	}

	// Create peer resolver function for domain endpoints
	// Always use system resolver for peer endpoints because:
	// 1. VPN server must be resolved before VPN tunnel is established
	// 2. dnsRouter may not be fully initialized at this stage
	var resolvePeer func(domain string) (netip.Addr, error)
	if remoteIsDomain {
		resolvePeer = func(domain string) (netip.Addr, error) {
			addrs, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip", domain)
			if lookupErr != nil {
				return netip.Addr{}, lookupErr
			}
			if len(addrs) == 0 {
				return netip.Addr{}, E.New("no addresses returned for peer: ", domain)
			}
			return addrs[0], nil
		}
	}

	ipc, err := genIpcConfig(options, resolvePeer)
	if err != nil {
		return nil, err
	}

	logger.Debug("AWG IPC configuration prepared")

	dev, err := awg.NewDevice(ctx, logger, dial, ipc, awg.DeviceOpts{
		UseIntegratedTun: options.UseIntegratedTun,
		Address:          options.Address,
		AllowedIps:       allowedIps.Prefixes(),
		ExcludedIps:      excludedIps.Prefixes(),
		MTU:              options.MTU,
		Peer:             singlePeer(options.Peers),
		Fallback:         fallback,
		MTUDiscovery:     options.MTUDiscovery,
		Reserved:         reserved,
	})
	if err != nil {
		return nil, err
	}

	peerUsers := peeruser.New(common.Map(options.Peers, func(it option.AwgPeerOptions) peeruser.Peer {
		return peeruser.Peer{User: it.Username, AllowedIPs: it.AllowedIPs}
	}))
	return &Endpoint{
		Device:    dev,
		Adapter:   endpoint.NewAdapterWithDialerOptions("awg", tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions),
		address:   options.Address,
		peerUsers: peerUsers,
		router:    router,
		logger:    logger,
		dnsRouter: service.FromContext[adapter.DNSRouter](ctx),
		ctx:       ctx,
	}, nil
}

func genIpcConfig(opts option.AwgEndpointOptions, resolvePeer func(domain string) (netip.Addr, error)) (string, error) {
	privateKeyBytes, err := base64.StdEncoding.DecodeString(opts.PrivateKey)
	if err != nil {
		return "", err
	}
	var s strings.Builder
	s.WriteString("private_key=" + hex.EncodeToString(privateKeyBytes))
	if opts.ListenPort != 0 {
		s.WriteString("\nlisten_port=" + format.ToString(opts.ListenPort))
	}
	awg := opts.Awg
	if awg.HeaderProtectionKey != "" {
		key, err := base64.StdEncoding.DecodeString(awg.HeaderProtectionKey)
		if err != nil || len(key) != 32 {
			return "", E.New("invalid AWG header protection key")
		}
		s.WriteString("\nheader_protection_key=" + hex.EncodeToString(key))
	}
	for _, field := range []struct{ name, value string }{
		{"content_padding_addition", awg.ContentPaddingAddition},
		{"rekey_after_time", awg.RekeyAfterTime},
		{"rekey_timeout", awg.RekeyTimeout},
		{"reject_after_time", awg.RejectAfterTime},
		{"keepalive_timeout", awg.KeepaliveTimeout},
		{"max_handshake_attempts", awg.MaxHandshakeAttempts},
	} {
		if field.value != "" {
			s.WriteString("\n" + field.name + "=" + field.value)
		}
	}
	if awg.RandomTrailers {
		s.WriteString("\nrandom_trailers=true")
	}
	if awg.DisableCookies {
		s.WriteString("\ndisable_cookies=true")
	}
	if awg.Jc != 0 {
		s.WriteString("\njc=" + format.ToString(awg.Jc))
	}
	if awg.Jmin != 0 {
		s.WriteString("\njmin=" + format.ToString(awg.Jmin))
	}
	if awg.Jmax != 0 {
		s.WriteString("\njmax=" + format.ToString(awg.Jmax))
	}
	if awg.S1 != 0 {
		s.WriteString("\ns1=" + format.ToString(awg.S1))
	}
	if awg.S2 != 0 {
		s.WriteString("\ns2=" + format.ToString(awg.S2))
	}
	if awg.S3 != 0 {
		s.WriteString("\ns3=" + format.ToString(awg.S3))
	}
	if awg.S4 != 0 {
		s.WriteString("\ns4=" + format.ToString(awg.S4))
	}
	if awg.H1 != "" {
		s.WriteString("\nh1=" + awg.H1)
	}
	if awg.H2 != "" {
		s.WriteString("\nh2=" + awg.H2)
	}
	if awg.H3 != "" {
		s.WriteString("\nh3=" + awg.H3)
	}
	if awg.H4 != "" {
		s.WriteString("\nh4=" + awg.H4)
	}
	if awg.I1 != "" {
		s.WriteString("\ni1=" + awg.I1)
	}
	if awg.I2 != "" {
		s.WriteString("\ni2=" + awg.I2)
	}
	if awg.I3 != "" {
		s.WriteString("\ni3=" + awg.I3)
	}
	if awg.I4 != "" {
		s.WriteString("\ni4=" + awg.I4)
	}
	if awg.I5 != "" {
		s.WriteString("\ni5=" + awg.I5)
	}

	for _, peer := range opts.Peers {
		publicKeyBytes, err := base64.StdEncoding.DecodeString(peer.PublicKey)
		if err != nil {
			return "", err
		}
		s.WriteString("\npublic_key=" + hex.EncodeToString(publicKeyBytes))
		if peer.PresharedKey != "" {
			presharedKeyBytes, err := base64.StdEncoding.DecodeString(peer.PresharedKey)
			if err != nil {
				return "", err
			}
			s.WriteString("\npreshared_key=" + hex.EncodeToString(presharedKeyBytes))
		}
		if peer.Address != "" && peer.Port != 0 {
			// Resolve domain to IP if necessary
			endpointAddr := peer.Address
			if addr := M.ParseAddr(peer.Address); !addr.IsValid() {
				// It's a domain, resolve it
				if resolvePeer == nil {
					return "", E.New("peer address is a domain but no resolver provided: ", peer.Address)
				}
				resolvedAddr, resolveErr := resolvePeer(peer.Address)
				if resolveErr != nil {
					return "", E.Cause(resolveErr, "resolve peer endpoint ", peer.Address)
				}
				endpointAddr = resolvedAddr.String()
			}
			s.WriteString("\nendpoint=" + net.JoinHostPort(endpointAddr, strconv.Itoa(int(peer.Port))))
		}
		if peer.PersistentKeepaliveInterval != 0 {
			s.WriteString("\npersistent_keepalive_interval=" + format.ToString(peer.PersistentKeepaliveInterval))
		}
		for _, allowedIp := range peer.AllowedIPs {
			s.WriteString("\nallowed_ip=" + allowedIp.String())
		}
	}
	return s.String(), nil
}

func (receiver *Endpoint) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = receiver.Tag()
	metadata.InboundType = receiver.Type()
	metadata.Source = source
	metadata.User = receiver.peerUsers.Lookup(source.Addr) // H
	metadata.Destination = destination
	for _, addr := range receiver.address {
		if addr.Contains(destination.Addr) {
			metadata.OriginDestination = destination
			if destination.Addr.Is4() {
				metadata.Destination.Addr = netip.AddrFrom4([4]uint8{127, 0, 0, 1})
			} else {
				metadata.Destination.Addr = netip.IPv6Loopback()
			}
			conn = bufio.NewNATPacketConn(bufio.NewNetPacketConn(conn), metadata.OriginDestination, metadata.Destination)
		}
	}
	receiver.logger.InfoContext(ctx, "inbound packet connection from ", source)
	receiver.logger.InfoContext(ctx, "inbound packet connection to ", destination)
	receiver.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

func (receiver *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch network {
	case N.NetworkTCP:
		receiver.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		receiver.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	if destination.IsDomain() {
		destinationAddresses, err := receiver.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, err
		}
		return N.DialSerial(ctx, receiver.Device, network, destination, destinationAddresses)
	} else if !destination.Addr.IsValid() {
		return nil, E.New("invalid destination: ", destination)
	}
	return receiver.Device.DialContext(ctx, network, destination)
}

func (receiver *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	receiver.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	if destination.IsDomain() {
		destinationAddresses, err := receiver.dnsRouter.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
		if err != nil {
			return nil, err
		}
		packetConn, _, err := N.ListenSerial(ctx, receiver.Device, destination, destinationAddresses)
		if err != nil {
			return nil, err
		}
		return packetConn, nil
	}
	return receiver.Device.ListenPacket(ctx, destination)
}

func (receiver *Endpoint) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = receiver.Tag()
	metadata.InboundType = receiver.Type()
	metadata.Source = source
	metadata.User = receiver.peerUsers.Lookup(source.Addr) // H
	for _, addr := range receiver.address {
		if addr.Contains(destination.Addr) {
			metadata.OriginDestination = destination
			if destination.Addr.Is4() {
				destination.Addr = netip.AddrFrom4([4]uint8{127, 0, 0, 1})
			} else {
				destination.Addr = netip.IPv6Loopback()
			}
			break
		}
	}
	metadata.Destination = destination
	receiver.logger.InfoContext(ctx, "inbound connection from ", source)
	receiver.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	receiver.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (receiver *Endpoint) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	switch stage {
	case adapter.StartStateInitialize:
		if receiver.Device != nil {
			scope.Add(receiver.Close)
		}
	case adapter.StartStateStart:
		return receiver.Device.Start(stage)
	case adapter.StartStatePostStart:
		go receiver.readyChecker()
	}
	return nil
}

func (receiver *Endpoint) readyChecker() {
	for range 10 {
		if receiver.deviceReady() {
			break
		}
		select {
		case <-receiver.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	receiver.started = true
	if m := monitoring.Get(receiver.ctx); m != nil {
		m.TestNow(receiver.Tag())
	}
}

func (receiver *Endpoint) deviceReady() bool {
	return receiver.Device != nil && receiver.Device.IsUnderLoad()
}

func (receiver *Endpoint) IsReady() bool {
	return receiver.started || receiver.deviceReady()
}

func (receiver *Endpoint) DisplayType() string {
	str := C.ProxyDisplayName(receiver.Type())
	if !receiver.IsReady() {
		str += " ⚠️ Connecting..."
	}
	return str
}
