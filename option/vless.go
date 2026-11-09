package option

type VLESSInboundOptions struct {
	Testseed []uint32 `json:"testseed,omitempty"`
	ListenOptions
	Users      []VLESSUser `json:"users,omitempty"`
	Decryption string      `json:"decryption,omitempty"` // H
	InboundTLSOptionsContainer
	Multiplex *InboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport *V2RayTransportOptions   `json:"transport,omitempty"`
}

type VLESSReverseOptions struct {
	Tag string `json:"tag"`
}

type VLESSReversePortalOptions struct{}

type VLESSUser struct {
	Reverse *VLESSReverseOptions `json:"reverse,omitempty"`
	Name    string               `json:"name,omitempty"`
	UUID    string               `json:"uuid"`
	Flow    string               `json:"flow,omitempty"`
}

type VLESSOutboundOptions struct {
	Reverse  *VLESSReverseOptions `json:"reverse,omitempty"`
	Testseed []uint32             `json:"testseed,omitempty"`
	Testpre  uint32               `json:"testpre,omitempty"`
	DialerOptions
	ServerOptions
	UUID       string      `json:"uuid"`
	Flow       string      `json:"flow,omitempty"`
	Encryption string      `json:"encryption,omitempty"` // H
	Network    NetworkList `json:"network,omitempty"`
	OutboundTLSOptionsContainer
	Multiplex      *OutboundMultiplexOptions `json:"multiplex,omitempty"`
	Transport      *V2RayTransportOptions    `json:"transport,omitempty"`
	PacketEncoding *string                   `json:"packet_encoding,omitempty"`
}
