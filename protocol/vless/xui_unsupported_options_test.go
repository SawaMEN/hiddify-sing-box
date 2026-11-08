package vless

import (
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestXUIPanelUnsupportedVLESSProtectionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options option.VLESSOutboundOptions
	}{
		{name: "outbound-testseed", options: option.VLESSOutboundOptions{Testseed: []uint32{1000, 1}}},
		{name: "outbound-reverse", options: option.VLESSOutboundOptions{Reverse: &option.VLESSReverseOptions{Tag: "portal"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOutbound(context.Background(), nil, nil, "test", tc.options)
			if err == nil || !strings.Contains(err.Error(), "requires") {
				t.Fatalf("unsupported config accepted: %v", err)
			}
		})
	}
	_, err := NewInbound(context.Background(), nil, nil, "test", option.VLESSInboundOptions{Testseed: []uint32{1000}})
	if err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("inbound Vision testseed was silently ignored: %v", err)
	}
	_, err = NewInbound(context.Background(), nil, nil, "test", option.VLESSInboundOptions{Users: []option.VLESSUser{{UUID: "11111111-1111-4111-8111-111111111111", Reverse: &option.VLESSReverseOptions{Tag: "portal"}}}})
	if err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("inbound reverse user was silently ignored: %v", err)
	}
}
