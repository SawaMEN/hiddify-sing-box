package vless_test

import (
	"testing"

	"github.com/sagernet/sing-vmess/vless"
)

func TestXUINativeVisionTestseedAndReverseAPI(t *testing.T) {
	t.Parallel()
	seed, err := vless.ValidateVisionTestseed(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := [4]uint32{900, 500, 900, 256}
	if seed != want {
		t.Fatalf("default testseed: got %v, want %v", seed, want)
	}
	if vless.CommandReverse != byte(4) {
		t.Fatalf("reverse command: got %d, want 4", vless.CommandReverse)
	}
	for _, bad := range [][]uint32{
		{1},
		{0, 1, 1, 1},
		{900, 0, 900, 256},
		{900, 500, 800, 256},
		{900, 65000, 900, 256},
		{900, 500, 900, 65537},
	} {
		if _, err := vless.ValidateVisionTestseed(bad); err == nil {
			t.Errorf("invalid testseed %v was accepted", bad)
		}
	}
	seed, err = vless.ValidateVisionTestseed([]uint32{800, 300, 800, 128})
	if err != nil {
		t.Fatal(err)
	}
	want = [4]uint32{800, 300, 800, 128}
	if seed != want {
		t.Fatalf("custom testseed: got %v, want %v", seed, want)
	}
}
