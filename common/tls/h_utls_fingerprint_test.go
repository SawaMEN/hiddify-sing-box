//go:build with_utls

package tls

import (
	"testing"

	utls "github.com/metacubex/utls"
	"github.com/stretchr/testify/require"
)

// The native uTLS copy exposes verified browser ClientHello snapshots under
// stable fingerprint names. Chrome 155 replaced Chrome 133 rather than adding
// another selectable fingerprint; the other snapshots are Firefox 148 and Safari 26.3.
func TestH_UTLSFingerprintVersions(t *testing.T) {
	for name, want := range map[string]utls.ClientHelloID{
		"chrome":  utls.HelloChrome_155,
		"firefox": utls.HelloFirefox_148,
		"safari":  utls.HelloSafari_26_3,
	} {
		id, err := uTLSClientHelloID(name)
		require.NoError(t, err)
		require.Equal(t, want, id, name)
	}
}
