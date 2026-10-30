package option

// VetrOFFOutboundOptions stores the canonical connection link, including
// credentials, for the native VetroffProxy SDK. Never log this field.
type VetrOFFOutboundOptions struct {
    Link string `json:"link"`
}
