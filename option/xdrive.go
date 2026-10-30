package option

// V2RayXDriveOptions configures storage carrying the encrypted VLESS stream.
// These are native sing-box transport options, not an external Xray process.
type V2RayXDriveOptions struct {
	FrontAddress      string                 `json:"front_address,omitempty"`
	FrontPort         uint16                 `json:"front_port,omitempty"`
	Service           string                 `json:"service"`
	RemoteFolder      string                 `json:"remote_folder"`
	Secrets           []string               `json:"secrets,omitempty"`
	SegmentBytes      uint32                 `json:"segment_bytes,omitempty"`
	FlushIntervalMs   uint32                 `json:"flush_interval_ms,omitempty"`
	PollIntervalMs    uint32                 `json:"poll_interval_ms,omitempty"`
	MaxPollIntervalMs uint32                 `json:"max_poll_interval_ms,omitempty"`
	SessionTtlSeconds uint32                 `json:"session_ttl_seconds,omitempty"`
	Concurrency       uint32                 `json:"concurrency,omitempty"`
	EagerWindowMs     uint32                 `json:"eager_window_ms,omitempty"`
	HoleTimeoutMs     uint32                 `json:"hole_timeout_ms,omitempty"`
	Template          string                 `json:"template,omitempty"`
	Xmux              *V2RayXHTTPXmuxOptions `json:"xmux,omitempty"`
	S3                *XDriveS3Options       `json:"s3,omitempty"`
	StorageDialer     DialerOptions          `json:"storage_dialer,omitempty"`
}

type XDriveS3Options struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket"`
	PathStyle bool   `json:"path_style,omitempty"`
}
