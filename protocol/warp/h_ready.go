package warp

import (
	"time"

	C "github.com/sagernet/sing-box/constant"
)

// firstHandshakeWait is how long a new endpoint has to complete its first handshake before
// the next port is tried.
var firstHandshakeWait = 10 * time.Second

// rotatePorts runs when the scanner is off and no server_port is set: when an endpoint does
// not complete a handshake, the next of the profile's ports is tried. A port that does not
// answer on a network was used forever before.
func (w *WARPEndpoint) rotatePorts(done <-chan struct{}, config *C.WARPConfig, address string, ports []uint16, current int) {
	connectedAt := time.Now()
	wait := firstHandshakeWait
	for {
		select {
		case <-done:
			return
		case <-w.ctx.Done():
			return
		case <-time.After(wait):
		}
		inner := w.currentEndpoint()
		if inner == nil {
			return
		}
		lastHandshake, _ := peerStatus(inner)
		if lastHandshake.After(connectedAt) && time.Since(lastHandshake) < staleHandshake {
			wait = failoverInterval // answering: check again later
			continue
		}
		current = (current + 1) % len(ports)
		if err := w.replaceInner(done, config, address, ports[current]); err != nil {
			w.logger.ErrorContext(w.ctx, err)
			return
		}
		w.logger.InfoContext(w.ctx, "WARP: no handshake, trying port ", ports[current])
		connectedAt = time.Now()
		wait = firstHandshakeWait
	}
}
