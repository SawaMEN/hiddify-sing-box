package awg

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/network"

	awgTun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

type tunAdapter interface {
	network.Dialer
	awgTun.Device
	adapter.SimpleLifecycle
}
