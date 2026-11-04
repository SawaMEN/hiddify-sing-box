package option

import (
	"testing"

	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func TestH_XDriveLegacyTemplate(t *testing.T) {
	for _, encoded := range []string{
		`{"service":"template","template":"{\"put\":{\"url\":\"https://storage.example/put\"}}","front_address":"192.0.2.1","front_port":443}`,
		`{"service":"template","template":{"put":{"url":"https://storage.example/put"}},"front_address":"192.0.2.1","front_port":443}`,
	} {
		var options V2RayXDriveOptions
		require.NoError(t, json.Unmarshal([]byte(encoded), &options))
		require.JSONEq(t, `{"put":{"url":"https://storage.example/put"}}`, string(options.Template))
		require.Equal(t, "192.0.2.1", options.FrontAddress)
		require.EqualValues(t, 443, options.FrontPort)
	}
}
