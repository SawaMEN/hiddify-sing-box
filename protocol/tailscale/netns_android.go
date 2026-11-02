//go:build with_tailscale && android

package tailscale

import (
	"github.com/sagernet/tailscale/net/netns"
)

func setAndroidProtectFunc(f func(fd int) error) {
	netns.SetAndroidProtectFunc(f)
}
