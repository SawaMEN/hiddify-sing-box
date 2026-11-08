package vless_test

import (
	"testing"

	"github.com/sagernet/sing-vmess/vless"
	"github.com/stretchr/testify/require"
)

func TestXUINativeVisionTestseedAndReverseAPI(t *testing.T) {
	t.Parallel()
	seed, err := vless.ValidateVisionTestseed(nil)
	require.NoError(t, err)
	require.Equal(t, [4]uint32{900, 500, 900, 256}, seed)
	require.Equal(t, byte(4), vless.CommandReverse)
	for _, bad := range [][]uint32{
		{1}, {0, 1, 1, 1}, {900, 0, 900, 256},
		{900, 500, 800, 256}, {900, 65000, 900, 256}, {900, 500, 900, 65537},
	} {
		_, err := vless.ValidateVisionTestseed(bad)
		require.Error(t, err, "testseed %v must be rejected", bad)
	}
	seed, err = vless.ValidateVisionTestseed([]uint32{800, 300, 800, 128})
	require.NoError(t, err)
	require.Equal(t, [4]uint32{800, 300, 800, 128}, seed)
}
