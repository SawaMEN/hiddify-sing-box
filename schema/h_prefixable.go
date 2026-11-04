package schema

import (
	"reflect"

	E "github.com/sagernet/sing/common/exceptions"
)

// hPrefixableField describes the fork's string-backed CIDR options accurately.
func (g *generator) hPrefixableField(fieldType reflect.Type) (*Node, error) {
	listable := isListable(fieldType)
	valueType := fieldType
	if listable {
		valueType = fieldType.Elem()
	}
	if valueType.Kind() != reflect.String {
		return nil, E.New("prefixable schema tag requires strings")
	}
	node, err := g.Describe(prefixableType)
	if err != nil {
		return nil, err
	}
	if listable {
		return ListableOf(node), nil
	}
	return node, nil
}
