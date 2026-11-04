//go:build with_utls

package tls

import (
	"testing"

	utls "github.com/metacubex/utls"
	"github.com/stretchr/testify/require"
)

// Check the stable uTLS fingerprint selectors used in the current build.
// Auto IDs are supported by the pinned uTLS fork without tying the code
// to version-specific constants that may be removed or renamed upstream.
func TestH_UTLSFingerprintVersions(t *testing.T) {
	for name, want := range map[string]utls.ClientHelloID{
		"chrome":  utls.HelloChrome_Auto,
		"firefox": utls.HelloFirefox_Auto,
		"safari":  utls.HelloSafari_Auto,
	} {
		id, err := uTLSClientHelloID(name)
		require.NoError(t, err)
		require.Equal(t, want, id, name)
	}
}
