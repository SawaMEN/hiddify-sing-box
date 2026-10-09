package dialer

import "testing"

func TestExplicitPlatformProtectionWhenAutoDetectDisabled(t *testing.T) {
	if !needsExplicitPlatformProtection(false) {
		t.Fatal("Android VPN outbound sockets must still be protected without auto_detect_interface")
	}
}

func TestNoDuplicatePlatformProtectionWithAutoDetect(t *testing.T) {
	if needsExplicitPlatformProtection(true) {
		t.Fatal("auto_detect_interface already installs the platform socket control")
	}
}
