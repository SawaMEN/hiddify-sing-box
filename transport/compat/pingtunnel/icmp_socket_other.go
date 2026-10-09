//go:build !linux && !android

package pingtunnel

import "fmt"

func listenICMP(addr string, protect func(uintptr) error) (*PacketConn, error) {
	return nil, fmt.Errorf("pingtunnel: native ICMP support requires Linux or Android")
}
