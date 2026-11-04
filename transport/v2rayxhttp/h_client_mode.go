package xhttp

// hClientMode follows Xray's auto-mode policy before creating session metadata.
func hClientMode(mode string, reality bool, separateDownload bool) string {
	if mode != "auto" {
		return mode
	}
	if reality {
		if separateDownload {
			return "stream-up"
		}
		return "stream-one"
	}
	return "packet-up"
}
