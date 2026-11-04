package include

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func TestH_BondOutboundRegistered(t *testing.T) {
	options, loaded := OutboundRegistry().CreateOptions(C.TypeBond)
	require.True(t, loaded, "bond outbound must be registered with and without with_bond")
	require.IsType(t, &option.BondOutboundOptions{}, options)
}
