package shadowsocks

import (
	"context"
	"net"
	"sync"
)

// obfsRecordLimit bounds what is kept for replay. A Shadowsocks header and the
// obfs request around it are far smaller; a client that sends more without
// authenticating is just closed.
const obfsRecordLimit = 16 << 10

type obfsRecorderKey struct{}

// markAuthenticated is called by the inbounds' connection handlers, which the
// Shadowsocks service only reaches once the client has authenticated. From then on
// nothing is recorded, and a later failure closes the connection instead of replaying it.
func markAuthenticated(ctx context.Context) {
	if recorder, loaded := ctx.Value(obfsRecorderKey{}).(*obfsRecorder); loaded {
		recorder.authenticate()
	}
}

// obfsRecorder keeps what the client sent until it authenticates.
type obfsRecorder struct {
	net.Conn
	access        sync.Mutex
	recorded      []byte
	authenticated bool
	overflow      bool
}

func (r *obfsRecorder) Read(b []byte) (int, error) {
	n, err := r.Conn.Read(b)
	if n > 0 {
		r.access.Lock()
		if !r.authenticated && !r.overflow {
			if len(r.recorded)+n > obfsRecordLimit {
				r.overflow = true
				r.recorded = nil
			} else {
				r.recorded = append(r.recorded, b[:n]...)
			}
		}
		r.access.Unlock()
	}
	return n, err
}

func (r *obfsRecorder) authenticate() {
	r.access.Lock()
	r.authenticated = true
	r.recorded = nil
	r.access.Unlock()
}

func (r *obfsRecorder) Upstream() any {
	return r.Conn
}
