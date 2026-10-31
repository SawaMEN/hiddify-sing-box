package srs

import (
	"bytes"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// A recovered binary rule set must use the source JSON field types, not
// internal prefix pointer types. Both source and destination CIDRs round-trip.
func TestH_RecoverCIDRFields(t *testing.T) {
	original := option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				SourceIPCIDR: []string{"192.0.2.0/24"},
				IPCIDR:       []string{"2001:db8::/32"},
			},
		}},
	}
	var data bytes.Buffer
	require.NoError(t, Write(&data, original, C.RuleSetVersionCurrent))

	recovered, err := Read(bytes.NewReader(data.Bytes()), true)
	require.NoError(t, err)
	require.Len(t, recovered.Options.Rules, 1)
	rule := recovered.Options.Rules[0].DefaultOptions
	require.Equal(t, []string{"192.0.2.0/24"}, []string(rule.SourceIPCIDR))
	require.Equal(t, []string{"2001:db8::/32"}, []string(rule.IPCIDR))
}
