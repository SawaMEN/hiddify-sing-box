package vless

import (
	"testing"

	vmess "github.com/sagernet/sing-vmess/vless"
)

func TestXUIPanelVisionTestseedNativeValidation(t *testing.T) {
	for _, seed := range [][]uint32{
		nil, {900, 500, 900, 256}, {512, 256, 512, 256}, {900, 500, 900, 65536},
	} {
		if _, err := vmess.ValidateVisionTestseed(seed); err != nil {
			t.Fatalf("valid testseed %v rejected: %v", seed, err)
		}
	}
	for _, seed := range [][]uint32{
		{1},
		{900, 0, 900, 256},
		{0, 500, 900, 256},
		{900, 500, 898, 256}, {900, 500, 900, 65537},
		{900, 65535, 900, 256},
	} {
		if _, err := vmess.ValidateVisionTestseed(seed); err == nil {
			t.Fatalf("invalid testseed %v accepted", seed)
		}
	}
}
