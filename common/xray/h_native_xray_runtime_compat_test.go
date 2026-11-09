package common

import (
	"testing"

	XR "github.com/xtls/xray-core/app/router"
)

// Verify that the pinned Xray module provides native least-load parsing,
// including the bounds and strict schema used by panel balancers.
func TestH_NativeXrayRuntimeLeastLoadSettings(t *testing.T) {
	config, err := XR.ParseSingBoxLeastLoadSettings([]byte(`{"expected":-3,"tolerance":2.5}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Expected != 0 || config.Tolerance != 1 {
		t.Fatalf("least-load bounds not enforced: expected=%d tolerance=%v", config.Expected, config.Tolerance)
	}
	config, err = XR.ParseSingBoxLeastLoadSettings([]byte(`{"expected":3,"tolerance":-0.1}`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Expected != 3 || config.Tolerance != 0 {
		t.Fatalf("negative tolerance not rejected: expected=%d tolerance=%v", config.Expected, config.Tolerance)
	}
	if _, err = XR.ParseSingBoxLeastLoadSettings([]byte(`{"unexpected":1}`)); err == nil {
		t.Fatal("least-load parser silently ignored an unknown field")
	}
}
