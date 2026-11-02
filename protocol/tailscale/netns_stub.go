//go:build with_tailscale && !android

package tailscale

func setAndroidProtectFunc(f func(fd int) error) {
}
