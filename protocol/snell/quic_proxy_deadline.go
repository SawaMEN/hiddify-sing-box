package snell

import (
	"context"
	"os"
	"sync"
	"time"
)

// A zero-value deadline supports updates while a read is waiting for startup
// or queued datagrams; a single snapshot timer alone would miss those updates.
type quicProxyDeadline struct {
	mu      sync.Mutex
	at      time.Time
	changed chan struct{}
}

func (d *quicProxyDeadline) set(at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.at = at
	if d.changed != nil {
		close(d.changed)
	}
	d.changed = make(chan struct{})
}

func (d *quicProxyDeadline) snapshot() (time.Time, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.changed == nil {
		d.changed = make(chan struct{})
	}
	return d.at, d.changed
}

func (d *quicProxyDeadline) expired() bool {
	at, _ := d.snapshot()
	return !at.IsZero() && !time.Now().Before(at)
}

func (d *quicProxyDeadline) wait(ctx context.Context, ready <-chan struct{}) error {
	for {
		select {
		case <-ready:
			return nil
		default:
		}
		at, changed := d.snapshot()
		if !at.IsZero() && !time.Now().Before(at) {
			return os.ErrDeadlineExceeded
		}
		var timer *time.Timer
		var timeout <-chan time.Time
		if !at.IsZero() {
			timer = time.NewTimer(time.Until(at))
			timeout = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			select {
			case <-ready:
				return nil
			default:
				return ctx.Err()
			}
		case <-ready:
			if timer != nil {
				timer.Stop()
			}
			return nil
		case <-timeout:
			return os.ErrDeadlineExceeded
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
		}
	}
}
