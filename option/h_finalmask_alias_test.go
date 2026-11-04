package option

import (
	"testing"

	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func TestH_FinalMaskAliases(t *testing.T) {
	for _, key := range []string{"final_mask", "finalmask"} {
		t.Run(key, func(t *testing.T) {
			var options DialerOptions
			err := json.Unmarshal([]byte(`{"`+key+`":{"tcp":[{"type":"fragment"}]}}`), &options)
			require.NoError(t, err)
			require.NotNil(t, options.FinalMaskValue())
			require.Len(t, options.FinalMaskValue().TCP, 1)
		})
	}
	legacy := &FinalMaskOptions{TCP: []FinalMaskItem{{Type: "fragment"}}}
	options := DialerOptions{FinalMask: legacy, FinalMaskXray: &FinalMaskOptions{}}
	require.Same(t, legacy, options.FinalMaskValue())
}
