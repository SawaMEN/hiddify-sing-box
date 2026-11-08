package box_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

// Exercise both sides over real sockets: a translated FinalMask inbound must
// decode the same wire format emitted by the existing Xray-compatible dialer.
func TestXUIPanelInboundFinalMaskRoundTrip(t *testing.T) {
	for name, mask := range map[string]string{
		"fragment":      `{"tcp":[{"type":"fragment","settings":{"packets":"1-3","length":"1-4","delay":"0"}}]}`,
		"header-custom": `{"tcp":[{"type":"header-custom","settings":{"clients":[[{"type":"str","packet":"XUI-client\r\n"}]],"servers":[[{"type":"str","packet":"XUI-server\r\n"}]]}}]}`,
		"sudoku":        `{"tcp":[{"type":"sudoku","settings":{"password":"round-trip-secret","ascii":"prefer_entropy","paddingMin":1,"paddingMax":10}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			echo, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer echo.Close()
			go func() {
				c, err := echo.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				io.Copy(c, c)
			}()
			reserve, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := reserve.Addr().(*net.TCPAddr).Port
			require.NoError(t, reserve.Close())
			config := fmt.Sprintf(`{"log":{"disabled":true},"inbounds":[{"type":"vless","tag":"server","listen":"127.0.0.1","listen_port":%d,"users":[{"uuid":"11111111-1111-4111-8111-111111111111"}],"final_mask":%s}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"vless","tag":"client","server":"127.0.0.1","server_port":%d,"uuid":"11111111-1111-4111-8111-111111111111","final_mask":%s}],"route":{"final":"direct"}}`, port, mask, port, mask)
			ctx := include.Context(context.Background())
			ctx = service.ContextWith[adapter.PlatformInterface](ctx, &xuiLoopbackPlatform{})
			options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
			require.NoError(t, err)
			instance, err := box.New(box.Options{Context: ctx, Options: options})
			require.NoError(t, err)
			defer instance.Close()
			require.NoError(t, instance.Start())
			client, ok := instance.Outbound().Outbound("client")
			require.True(t, ok)
			dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			conn, err := client.DialContext(dialCtx, N.NetworkTCP, M.SocksaddrFromNet(echo.Addr()))
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
			payload := []byte("FinalMask must survive VLESS authentication and both wire directions")
			_, err = conn.Write(payload)
			require.NoError(t, err)
			got := make([]byte, len(payload))
			_, err = io.ReadFull(conn, got)
			require.NoError(t, err)
			require.Equal(t, payload, got)
		})
	}
}

func TestXUIPanelInboundFinalMaskCheckRejectsInvalid(t *testing.T) {
	for _, entry := range []struct{ mask, extra string }{
		{`{"tcp":[{"type":"noise"}]}`, ``},
		{`{"tcp":[{"type":"fragment","settings":{"length":"bad"}}]}`, ``},
		{`{"udp":[{"type":"salamander","settings":{"password":"p"}}]}`, ``},
		{`{"tcp":[{"type":"fragment","settings":{"packets":"1-1","length":"1-4","delay":"0"}}]}`, `,"transport":{"type":"kcp"}`},
		{`{"tcp":[{"type":"fragment","settings":{"packets":"1-1","length":"1-4","delay":"0"}}]}`, `,"tls":{"enabled":true,"reality":{"enabled":true}}`},
	} {
		ctx := include.Context(context.Background())
		config := fmt.Sprintf(`{"inbounds":[{"type":"vless","listen":"127.0.0.1","listen_port":12345,"users":[{"uuid":"11111111-1111-4111-8111-111111111111"}],"final_mask":%s%s}]}`, entry.mask, entry.extra)
		options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
		require.NoError(t, err)
		instance, err := box.New(box.Options{Context: ctx, Options: options})
		if instance != nil {
			instance.Close()
		}
		require.Error(t, err, "check accepted an invalid server mask: %s", config)
	}
}

// These loopback tests do not need host interface discovery or a route monitor.
// Providing the platform interface keeps them runnable in unprivileged CI.
type xuiLoopbackPlatform struct{ adapter.PlatformInterface }

func (*xuiLoopbackPlatform) Initialize(adapter.NetworkManager) error  { return nil }
func (*xuiLoopbackPlatform) UsePlatformDefaultInterfaceMonitor() bool { return true }
func (*xuiLoopbackPlatform) CreateDefaultInterfaceMonitor(logger.Logger) tun.DefaultInterfaceMonitor {
	return nil
}
func (*xuiLoopbackPlatform) UsePlatformNetworkInterfaces() bool                     { return true }
func (*xuiLoopbackPlatform) NetworkInterfaces() ([]adapter.NetworkInterface, error) { return nil, nil }
func (*xuiLoopbackPlatform) UsePlatformAutoDetectInterfaceControl() bool            { return false }
func (*xuiLoopbackPlatform) UsePlatformConnectionOwnerFinder() bool                 { return false }
func (*xuiLoopbackPlatform) UsePlatformWIFIMonitor() bool                           { return false }
func (*xuiLoopbackPlatform) UsePlatformNeighborResolver() bool                      { return false }
func (*xuiLoopbackPlatform) UnderNetworkExtension() bool                            { return false }
func (*xuiLoopbackPlatform) NetworkExtensionIncludeAllNetworks() bool               { return false }
func (*xuiLoopbackPlatform) SystemCertificates() []string                           { return nil }
func (*xuiLoopbackPlatform) ClearDNSCache()                                         {}
