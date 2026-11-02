package option

// OlcRTCOutboundOptions configures the local olcRTC WebRTC-to-SOCKS5 bridge.
// The key and room are peer credentials, not a direct dial address.
// Neither the application nor sing-box may log the full contents.
type OlcRTCOutboundOptions struct {
	Provider        string `json:"provider"`
	Transport       string `json:"transport"`
	RoomID          string `json:"room_id"`
	Key             string `json:"key"`
	DNSServer       string `json:"dns_server,omitempty"`
	ProviderToken   string `json:"provider_token,omitempty"`
	VP8FPS          int    `json:"vp8_fps,omitempty"`
	VP8Batch        int    `json:"vp8_batch,omitempty"`
	SEIFPS          int    `json:"sei_fps,omitempty"`
	SEIBatch        int    `json:"sei_batch,omitempty"`
	SEIFragment     int    `json:"sei_fragment,omitempty"`
	SEIAckTimeout   int    `json:"sei_ack_timeout_ms,omitempty"`
	VideoWidth      int    `json:"video_width,omitempty"`
	VideoHeight     int    `json:"video_height,omitempty"`
	VideoFPS        int    `json:"video_fps,omitempty"`
	VideoCodec      string `json:"video_codec,omitempty"`
	VideoQRSize     int    `json:"video_qr_size,omitempty"`
	VideoQRRecovery string `json:"video_qr_recovery,omitempty"`
	VideoTileModule int    `json:"video_tile_module,omitempty"`
	VideoTileRS     int    `json:"video_tile_rs,omitempty"`
}
