package option

import (
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/json/badoption"
	hiddify "github.com/sagernet/wireguard-go/hiddify"
)

type WireGuardEndpointOptions struct {
	System          bool                             `json:"system,omitempty"`
	Name            string                           `json:"name,omitempty"`
	MTU             uint32                           `json:"mtu,omitempty"`
	Address         badoption.Listable[netip.Prefix] `json:"address"`
	PrivateKey      string                           `json:"private_key"`
	ListenPort      uint16                           `json:"listen_port,omitempty"`
	Peers           []WireGuardPeer                  `json:"peers,omitempty"`
	UDPTimeout      badoption.Duration               `json:"udp_timeout,omitempty"`
	UDPMapping      UDPNATBehavior                   `json:"udp_mapping,omitempty"`
	UDPFiltering    UDPNATBehavior                   `json:"udp_filtering,omitempty"`
	UDPNATMax       uint32                           `json:"udp_nat_max,omitempty"`
	Workers         int                              `json:"workers,omitempty"`
	OnDemand        bool                             `json:"on_demand,omitempty"`
	FallbackAddress badoption.Listable[netip.Addr]   `json:"fallback_address,omitempty"` // H
	DialerOptions

	Noise hiddify.NoiseOptions `json:"noise,omitempty"` // H
	AWG   *AwgOptions          `json:"awg,omitempty"`   // H
}

type WireGuardPeer struct {
	// H: connections from this peer are accounted to this user (metadata.User)
	Username                    string                           `json:"name,omitempty"`
	Address                     string                           `json:"address,omitempty"`
	Port                        uint16                           `json:"port,omitempty"`
	PublicKey                   string                           `json:"public_key,omitempty"`
	PreSharedKey                string                           `json:"pre_shared_key,omitempty"`
	AllowedIPs                  badoption.Listable[netip.Prefix] `json:"allowed_ips,omitempty"`
	PersistentKeepaliveInterval uint16                           `json:"persistent_keepalive_interval,omitempty"`
	Reserved                    []uint8                          `json:"reserved,omitempty"`
}

type WARPEndpointOptions struct { // H
	System      bool                `json:"system,omitempty"`
	Name        string              `json:"name,omitempty"`
	ListenPort  uint16              `json:"listen_port,omitempty"`
	UDPTimeout  badoption.Duration  `json:"udp_timeout,omitempty"`
	Workers     int                 `json:"workers,omitempty"`
	Profile     WARPProfile         `json:"profile,omitempty"`
	Mode        string              `json:"mode,omitempty"`
	Scanner     *WARPScannerOptions `json:"scanner,omitempty"`
	MASQUE      *WARPMASQUEOptions  `json:"masque,omitempty"`
	RotatePorts *bool               `json:"rotate_ports,omitempty"`
	DialerOptions

	UniqueIdentifier string `json:"unique_identifier,omitempty"`
	ServerOptions
	Noise hiddify.NoiseOptions `json:"noise,omitempty"`
	AWG   *AwgOptions          `json:"awg,omitempty"`
	*C.WARPConfig
	MTU uint32 `json:"mtu,omitempty"`
}

type WARPProfile struct { // H
	ID         string `json:"id,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	AuthToken  string `json:"auth_token,omitempty"`
	Recreate   bool   `json:"recreate,omitempty"`
	Detour     string `json:"detour,omitempty"`
	License    string `json:"license,omitempty"`
}

type LegacyWireGuardOutboundOptions struct { // H
	DialerOptions
	SystemInterface bool                             `json:"system_interface,omitempty"`
	GSO             bool                             `json:"gso,omitempty"`
	InterfaceName   string                           `json:"interface_name,omitempty"`
	LocalAddress    badoption.Listable[netip.Prefix] `json:"local_address"`
	PrivateKey      string                           `json:"private_key"`
	Peers           []LegacyWireGuardPeer            `json:"peers,omitempty"`
	ServerOptions
	PeerPublicKey string      `json:"peer_public_key"`
	PreSharedKey  string      `json:"pre_shared_key,omitempty"`
	Reserved      []uint8     `json:"reserved,omitempty"`
	Workers       int         `json:"workers,omitempty"`
	MTU           uint32      `json:"mtu,omitempty"`
	Network       NetworkList `json:"network,omitempty"`
}

type LegacyWireGuardPeer struct { // H
	ServerOptions
	PublicKey    string                           `json:"public_key,omitempty"`
	PreSharedKey string                           `json:"pre_shared_key,omitempty"`
	AllowedIPs   badoption.Listable[netip.Prefix] `json:"allowed_ips,omitempty"`
	Reserved     []uint8                          `json:"reserved,omitempty"`
}

// WARPMASQUEOptions selects WARP over MASQUE in the unified WARP endpoint.
type WARPMASQUEOptions struct {
	Enabled              bool                     `json:"enabled,omitempty"`
	VHTTP                string                   `json:"vhttp,omitempty"`
	TLS                  MASQUEOutboundTLSOptions `json:"tls,omitempty"`
	UseIPv6              bool                     `json:"use_ipv6,omitempty"`
	UDPKeepalivePeriod   badoption.Duration       `json:"udp_keepalive_period,omitempty"`
	UDPInitialPacketSize uint16                   `json:"udp_initial_packet_size,omitempty"`
	ReconnectDelay       badoption.Duration       `json:"reconnect_delay,omitempty"`
	ServerName           string                   `json:"server_name,omitempty"`
}

func (o *WARPMASQUEOptions) IsEnabled() bool { return o != nil && o.Enabled }
