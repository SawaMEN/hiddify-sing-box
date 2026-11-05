package option

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestH_WARPFallbackAddressCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"wg","fallback_address":["2606:4700:d0::1"]}`,
		`{"mode":"awg","fallback_address":"2606:4700:d0::1"}`,
	} {
		var options WARPEndpointOptions
		require.NoError(t, json.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(raw), &options))
		require.Equal(t, badoption.Listable[netip.Addr]{netip.MustParseAddr("2606:4700:d0::1")}, options.FallbackAddress)
		encoded, err := json.MarshalContext(context.Background(), options)
		require.NoError(t, err)
		var roundTrip WARPEndpointOptions
		require.NoError(t, json.UnmarshalContext(context.Background(), encoded, &roundTrip))
		require.Equal(t, options.FallbackAddress, roundTrip.FallbackAddress)
	}
}
