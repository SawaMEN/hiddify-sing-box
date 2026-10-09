//go:build linux || android

package pingtunnel

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"runtime"
)

func listenICMP(addr string, protect func(uintptr) error) (*PacketConn, error) {
	sockType := unix.SOCK_RAW
	if runtime.GOOS == "android" {
		sockType = unix.SOCK_DGRAM
	}
	fd, e := unix.Socket(unix.AF_INET, sockType|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMP)
	if e != nil {
		return nil, fmt.Errorf("pingtunnel: ICMP socket unavailable: %w", e)
	}
	file := os.NewFile(uintptr(fd), "pingtunnel-icmp")
	defer file.Close()
	if protect != nil {
		if e = protect(uintptr(fd)); e != nil {
			return nil, e
		}
	}
	var ip [4]byte
	if addr != "" && addr != "0.0.0.0" {
		a := net.ParseIP(addr).To4()
		if a == nil {
			return nil, fmt.Errorf("pingtunnel: invalid ICMP listen address")
		}
		copy(ip[:], a)
	}
	if e = unix.Bind(fd, &unix.SockaddrInet4{Addr: ip}); e != nil {
		return nil, e
	}
	p, e := net.FilePacketConn(file)
	if e != nil {
		return nil, e
	}
	setICMPDatagram(sockType == unix.SOCK_DGRAM)
	return &PacketConn{p}, nil
}
