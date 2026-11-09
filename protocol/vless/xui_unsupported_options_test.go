package vless

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/option"
	vlesswire "github.com/sagernet/sing-vmess/vless"
)

func TestXUIPanelNativeVLESSReverseOptions(t *testing.T) {
	var in option.VLESSInboundOptions
	if err := json.Unmarshal([]byte(`{"users":[{"uuid":"11111111-1111-4111-8111-111111111111","reverse":{"tag":"portal"}}],"testseed":[900,500,900,256]}`), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Users) != 1 || in.Users[0].Reverse == nil || in.Users[0].Reverse.Tag != "portal" {
		t.Fatalf("inbound reverse configuration lost: %+v", in.Users)
	}
	if _, err := vlesswire.ValidateVisionTestseed(in.Testseed); err != nil {
		t.Fatal(err)
	}

	var out option.VLESSOutboundOptions
	if err := json.Unmarshal([]byte(`{"reverse":{"tag":"bridge"},"testseed":[800,300,800,128]}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Reverse == nil || out.Reverse.Tag != "bridge" {
		t.Fatalf("outbound reverse configuration lost: %+v", out.Reverse)
	}
	if _, err := vlesswire.ValidateVisionTestseed(out.Testseed); err != nil {
		t.Fatal(err)
	}

	for _, invalid := range [][]uint32{
		{1000, 1},
		{900, 0, 900, 256},
		{900, 500, 800, 256},
	} {
		if _, err := vlesswire.ValidateVisionTestseed(invalid); err == nil {
			t.Errorf("invalid testseed %v was accepted", invalid)
		}
	}
}
