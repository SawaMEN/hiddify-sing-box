//go:build with_utls

package tls

import (
	"testing"

	utls "github.com/metacubex/utls"
)

// These mappings must match the ClientHello implementations in
// replace/utls-native, not just the names offered by the web panel.
func TestXUIBrowserFingerprintProfiles(t *testing.T) {
	tests := []struct {
		name string
		want utls.ClientHelloID
	}{
		{"", utls.HelloChrome_155},
		{"chrome", utls.HelloChrome_155},
		{"chrome_155", utls.HelloChrome_155},
		{"hellochrome_155", utls.HelloChrome_155},
		{"chrome_133", utls.HelloChrome_133},
		{"hellochrome_133", utls.HelloChrome_133},
		{"firefox", utls.HelloFirefox_148},
		{"firefox_148", utls.HelloFirefox_148},
		{"hellofirefox_148", utls.HelloFirefox_148},
		{"safari", utls.HelloSafari_26_3},
		{"safari_26_3", utls.HelloSafari_26_3},
		{"hellosafari_26_3", utls.HelloSafari_26_3},
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
