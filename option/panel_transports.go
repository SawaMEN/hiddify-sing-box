package option

type SudokuHTTPMaskOptions struct {
	Disable   bool   `json:"disable,omitempty"`
	Mode      string `json:"mode,omitempty"`
	TLS       bool   `json:"tls,omitempty"`
	Host      string `json:"host,omitempty"`
	PathRoot  string `json:"path_root,omitempty"`
	Multiplex string `json:"multiplex,omitempty"`
}
type SudokuOutboundOptions struct {
	DialerOptions
	ServerOptions
	Key                string                `json:"key"`
	ASCII              string                `json:"ascii,omitempty"`
	AEAD               string                `json:"aead,omitempty"`
	CustomTables       []string              `json:"custom_tables,omitempty"`
	EnablePureDownlink bool                  `json:"enable_pure_downlink,omitempty"`
	PaddingMin         int                   `json:"padding_min,omitempty"`
	PaddingMax         int                   `json:"padding_max,omitempty"`
	HTTPMask           SudokuHTTPMaskOptions `json:"httpmask,omitempty"`
}
type OpenFluxTransportOptions struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	Dial     string `json:"dial,omitempty"`
	Priority int    `json:"priority,omitempty"`
}
type OpenFluxOutboundOptions struct {
	DialerOptions
	Secret     string                     `json:"secret"`
	Context    string                     `json:"context,omitempty"`
	Codec      string                     `json:"codec,omitempty"`
	Negotiate  bool                       `json:"negotiate,omitempty"`
	Transports []OpenFluxTransportOptions `json:"transports"`
}
type FPTNOutboundOptions struct {
	DialerOptions
	ServerOptions
	Username    string `json:"username"`
	Password    string `json:"password"`
	Fingerprint string `json:"md5_fingerprint"`
	SNI         string `json:"sni,omitempty"`
	Bypass      string `json:"bypass,omitempty"`
	MTU         int    `json:"mtu,omitempty"`
}
type PingTunnelOutboundOptions struct {
	DialerOptions
	Server        string `json:"server"`
	Key           int    `json:"key,omitempty"`
	Encryption    string `json:"encryption,omitempty"`
	EncryptionKey string `json:"encryption_key,omitempty"`
}
