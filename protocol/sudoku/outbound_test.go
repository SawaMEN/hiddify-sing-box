package sudoku

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/stretchr/testify/require"
)

// The current Sudoku outbound uses the native v5 option shape, not the removed
// compat/sudoku client fields (ASCII, AEAD, HTTPMask.Disable).
func TestSudokuOutboundV5Options(t *testing.T) {
	const input = `{
		"server": "example.org",
		"server_port": 443,
		"key": "testing-key",
		"aead_method": "chacha20-poly1305",
		"table_type": "ascii",
		"padding_min": 10,
		"padding_max": 20,
		"enable_pure_downlink": true,
		"http_mask": {
			"enabled": true,
			"mode": "stream",
			"host": "example.org"
		}
	}`
	var options option.SudokuOutboundOptions
	require.NoError(t, json.Unmarshal([]byte(input), &options))
	require.Equal(t, "example.org", options.Server)
	require.Equal(t, uint16(443), options.ServerPort)
	require.Equal(t, "chacha20-poly1305", options.AEADMethod)
	require.Equal(t, "ascii", options.TableType)
	require.NotNil(t, options.PaddingMin)
	require.Equal(t, 10, *options.PaddingMin)
	require.NotNil(t, options.PaddingMax)
	require.Equal(t, 20, *options.PaddingMax)
	require.NotNil(t, options.EnablePureDownlink)
	require.True(t, *options.EnablePureDownlink)
	require.NotNil(t, options.HTTPMask)
	require.True(t, options.HTTPMask.Enabled)
	require.Equal(t, "example.org", options.HTTPMask.Host)
}
