//go:build with_utls

package tls

import (
	"testing"

	utls "github.com/metacubex/utls"
)

// These mappings must match the ClientHello implementations in
// replace/utls-native, not just the names offered by the web panel.
func TestXUIPanelBrowserFingerprintProfiles(t *testing.T) {
	tests := []struct {
		name string
		want utls.ClientHelloID
	}{
		{"", utls.HelloChrome_155},
		{"chrome", utls.HelloChrome_155},
		{"firefox", utls.HelloFirefox_148},
		{"safari", utls.HelloSafari_26_3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uTLSClientHelloID(tt.name)
			if err != nil {
				t.Fatalf("resolve fingerprint: %v", err)
			}
			if got.Client != tt.want.Client || got.Version != tt.want.Version {
				t.Fatalf("fingerprint %q resolves to %s/%s, want %s/%s",
					tt.name, got.Client, got.Version, tt.want.Client, tt.want.Version)
			}
			if _, err := utls.UTLSIdToSpec(got); err != nil {
				t.Fatalf("fingerprint %q cannot build a ClientHelloSpec: %v", tt.name, err)
			}
		})
	}
}

func TestXUIPanelBrowserFingerprintRejectFakePSKProfiles(t *testing.T) {
	// These were never separate PSK/PQ handshakes: silently returning
	// the Chrome profile was misleading, so reject them instead.
	for _, name := range []string{
		"chrome_psk",
		"chrome_psk_shuffle",
		"chrome_padding_psk_shuffle",
		"chrome_pq",
		"chrome_pq_psk",
	} {
		if _, err := uTLSClientHelloID(name); err == nil {
			t.Fatalf("obsolete fingerprint %q silently accepted", name)
		}
	}
}

func TestXUIPanelBrowserFingerprintRandomUsesVerifiedBrowser(t *testing.T) {
	got, err := uTLSClientHelloID("random")
	if err != nil {
		t.Fatal(err)
	}
	supported := []utls.ClientHelloID{utls.HelloChrome_155, utls.HelloFirefox_148, utls.HelloSafari_26_3}
	for _, fingerprint := range supported {
		if got.Client == fingerprint.Client && got.Version == fingerprint.Version {
			return
		}
	}
	t.Fatalf("random fingerprint selected an obsolete or unknown template: %s/%s", got.Client, got.Version)
}

func TestXUIPanelBrowserFingerprintRandomizedNoALPN(t *testing.T) {
	got, err := uTLSClientHelloID("randomizednoalpn")
	if err != nil {
		t.Fatal(err)
	}
	if got.Client != utls.HelloRandomizedNoALPN.Client || got.Version != utls.HelloRandomizedNoALPN.Version {
		t.Fatalf("unexpected randomized no-ALPN fingerprint: %s/%s", got.Client, got.Version)
	}
}
