//go:build with_utls

package tls

import (
	"testing"
	"net"

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

func TestXUIPanelBrowserFingerprintLegacyAliasesMigrate(t *testing.T) {
	for _, tc := range []struct {
		name string
		want utls.ClientHelloID
	}{
		{"chrome_psk", utls.HelloChrome_155},
		{"chrome_psk_shuffle", utls.HelloChrome_155},
		{"chrome_padding_psk_shuffle", utls.HelloChrome_155},
		{"chrome_pq", utls.HelloChrome_155},
		{"chrome_pq_psk", utls.HelloChrome_155},
		{"chrome_133", utls.HelloChrome_155},
		{"hellochrome_133", utls.HelloChrome_155},
		{"hellochrome_155", utls.HelloChrome_155},
		{"hellofirefox_148", utls.HelloFirefox_148},
		{"hellosafari_26_3", utls.HelloSafari_26_3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := uTLSClientHelloID(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if got.Client != tc.want.Client || got.Version != tc.want.Version {
				t.Fatalf("legacy alias %q returned %s/%s, want modern %s/%s", tc.name,
					got.Client, got.Version, tc.want.Client, tc.want.Version)
			}
		})
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

func TestXUIPanelBrowserFingerprintCanonicalClientHello(t *testing.T) {
	// Test every selectable uTLS profile as a real ClientHello generator.
	// No external server is required: BuildHandshakeState stays local.
	for _, name := range []string{
		"chrome", "firefox", "safari", "edge", "ios", "android",
		"360", "qq", "random", "randomized", "randomizednoalpn",
	} {
		t.Run(name, func(t *testing.T) {
			id, err := uTLSClientHelloID(name)
			if err != nil {
				t.Fatalf("fingerprint is not supported: %v", err)
			}
			conn := utls.UClient(&net.TCPConn{}, &utls.Config{
				ServerName: "example.com",
				NextProtos: []string{"h2", "http/1.1"},
			}, id)
			if err := conn.BuildHandshakeState(); err != nil {
				t.Fatalf("cannot construct ClientHello for %q: %v", name, err)
			}
			if len(conn.HandshakeState.Hello.Raw) == 0 {
				t.Fatalf("no ClientHello for %q", name)
			}
		})
	}
}
