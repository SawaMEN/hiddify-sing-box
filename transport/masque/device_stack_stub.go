//go:build !with_gvisor

package masque

import tun "github.com/sagernet/sing-tun"

func newStackDevice(options DeviceOptions) (Device, error) {
	return nil, tun.ErrGVisorNotIncluded
}
