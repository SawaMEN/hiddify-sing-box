//go:build !with_wireguard

package box_test

import "testing"

// hFreeUDPPort finds an available local UDP port for legacy and upstream
// WireGuard integration tests. The listener is released before the caller
// starts its own WireGuard endpoint.
func hFreeUDPPort(t *testing.T) int {
	t.Helper()
	port, release := hReserveUDPPort(t)
	release()
	return port
}
