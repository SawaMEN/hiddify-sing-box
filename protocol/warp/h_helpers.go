package warp

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// warpPeerEndpoint validates a WARP profile before attempting to construct an inner endpoint.
func warpPeerEndpoint(config *C.WARPConfig, server option.ServerOptions) (string, uint16, error) {
	if config == nil || len(config.Peers) == 0 || len(config.Peers[0].Endpoint.Ports) == 0 {
		return "", 0, E.New("invalid WARP profile: missing peer or endpoint ports")
	}
	peer := config.Peers[0]
	host := peer.Endpoint.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	} else if strings.Count(host, ":") == 1 {
		host, _, _ = strings.Cut(host, ":")
	}
	port := uint16(peer.Endpoint.Ports[0])
	if server.Server != "" {
		host = server.Server
	}
	if server.ServerPort != 0 {
		port = server.ServerPort
	}
	if host == "" || port == 0 {
		return "", 0, E.New("invalid WARP peer address or port")
	}
	return host, port, nil
}

// retry bounds each failed connection attempt and stops on endpoint shutdown or a new session.
func (w *WARPEndpoint) retry(done <-chan struct{}, label string, connect func() error) bool {
	for {
		select {
		case <-done:
			return false
		case <-w.ctx.Done():
			return false
		default:
		}
		if err := connect(); err == nil {
			return true
		} else if w.logger != nil {
			w.logger.WarnContext(w.ctx, label, ": ", err)
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-done:
			timer.Stop()
			return false
		case <-w.ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (w *WARPEndpoint) fail(err error) {
	if err != nil && w.logger != nil {
		w.logger.ErrorContext(w.ctx, err)
	}
}

// withWARPAPI uses the configured detour when specified; a missing detour never triggers
// a direct fallback. Otherwise it can retry over other available outbounds.
func withWARPAPI(ctx context.Context, profile *option.WARPProfile, call func(N.Dialer) error) error {
	manager := service.FromContext[adapter.OutboundManager](ctx)
	if profile != nil && profile.Detour != "" {
		if manager == nil {
			return E.New("WARP detour manager not available")
		}
		outbound, found := manager.Outbound(profile.Detour)
		if !found {
			return E.New("outbound detour not found: ", profile.Detour)
		}
		return call(outbound)
	}
	firstErr := call(nil)
	if firstErr == nil || manager == nil {
		return firstErr
	}
	for _, outbound := range manager.Outbounds() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := call(outbound); err == nil {
			return nil
		}
	}
	return firstErr
}
