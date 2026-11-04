package schema

import (
	"context"
	"reflect"
	"testing"

	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestH_PrefixableSchema(t *testing.T) {
	type options struct {
		CIDRs badoption.Listable[string] `json:"ip_cidr" schema:"prefixable"`
	}
	data, err := Generate(context.Background(), reflect.TypeFor[options]())
	require.NoError(t, err)
	require.Contains(t, string(data), `"ip_cidr"`)
	require.Contains(t, string(data), "IPAddressOrPrefix")
}

func TestH_PrefixableSchemaRejectsNonString(t *testing.T) {
	type options struct {
		CIDR int `json:"ip_cidr" schema:"prefixable"`
	}
	_, err := Generate(context.Background(), reflect.TypeFor[options]())
	require.ErrorContains(t, err, "prefixable schema tag requires strings")
}
