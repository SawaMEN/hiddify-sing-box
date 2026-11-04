package option

import (
	"bytes"

	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
)

// V2RayXDriveOptions configures the "xdrive" V2Ray transport, Xray-core's XDRIVE: the connection
// is carried in files of a storage service (Google Drive, S3, a "template" of HTTP requests, or a
// local folder) that the client and the server both reach, so no packet goes to the server. The
// fields are those of Xray's xdriveSettings.
type V2RayXDriveOptions struct {
	Service           string                     `json:"service,omitempty"` // google-drive, s3, template or local
	RemoteFolder      string                     `json:"remote_folder,omitempty"`
	Secrets           badoption.Listable[string] `json:"secrets,omitempty"`
	SegmentBytes      uint32                     `json:"segment_bytes,omitempty"`
	FlushIntervalMs   uint32                     `json:"flush_interval_ms,omitempty"`
	PollIntervalMs    uint32                     `json:"poll_interval_ms,omitempty"`
	MaxPollIntervalMs uint32                     `json:"max_poll_interval_ms,omitempty"`
	SessionTtlSeconds uint32                     `json:"session_ttl_seconds,omitempty"`
	Concurrency       uint32                     `json:"concurrency,omitempty"`
	EagerWindowMs     uint32                     `json:"eager_window_ms,omitempty"`
	HoleTimeoutMs     uint32                     `json:"hole_timeout_ms,omitempty"`
	Template          json.RawMessage            `json:"template,omitempty"`
	S3                *V2RayXDriveS3Options      `json:"s3,omitempty"`
	Xmux              *V2RayXHTTPXmuxOptions     `json:"xmux,omitempty"`
	// FrontingAddress and FrontingPort, when set, are dialed for every host of the service
	// instead of its own address, with the TLS server name still that host (Xray's
	// streamSettings "address" and "port").
	FrontingAddress string        `json:"fronting_address,omitempty"`
	FrontingPort    uint16        `json:"fronting_port,omitempty"`
	FrontAddress    string        `json:"front_address,omitempty"`  // H: legacy alias
	FrontPort       uint16        `json:"front_port,omitempty"`     // H: legacy alias
	StorageDialer   DialerOptions `json:"storage_dialer,omitempty"` // H: legacy storage routing
}

type V2RayXDriveS3Options struct {
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	PathStyle *bool  `json:"path_style,omitempty"`
}

// UnmarshalJSON also accepts the old JSON-encoded template string.
func (o *V2RayXDriveOptions) UnmarshalJSON(content []byte) error {
	type rawOptions V2RayXDriveOptions
	var value rawOptions
	if err := json.Unmarshal(content, &value); err != nil {
		return err
	}
	if len(value.Template) > 0 && bytes.HasPrefix(bytes.TrimSpace(value.Template), []byte("\"")) {
		var template string
		if err := json.Unmarshal(value.Template, &template); err != nil {
			return err
		}
		value.Template = json.RawMessage(template)
	}
	*o = V2RayXDriveOptions(value)
	return nil
}
