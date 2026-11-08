package vless

import (
	"context"
	"net"
	"sync"
	"time"
)

type preconnected struct {
	conn       net.Conn
	base       net.Conn
	expires    time.Time
	generation chan struct{}
}

type preconnectPool struct {
	ctx        context.Context
	cancel     context.CancelFunc
	dial       func(context.Context) (net.Conn, net.Conn, error)
	size       int
	once       sync.Once
	mu         sync.Mutex
	closed     bool
	generation chan struct{}
	ready      chan preconnected
	wg         sync.WaitGroup
}

// newPreconnectPool remains idle until the first request.
func newPreconnectPool(ctx context.Context, size int, dial func(context.Context) (net.Conn, net.Conn, error)) *preconnectPool {
	ctx, cancel := context.WithCancel(ctx)
	return &preconnectPool{
		ctx:        ctx,
		cancel:     cancel,
		dial:       dial,
		size:       size,
		ready:      make(chan preconnected),
		generation: make(chan struct{}),
	}
}

func (p *preconnectPool) Get(ctx context.Context) (net.Conn, net.Conn, error) {
	p.once.Do(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.closed || p.ctx.Err() != nil {
			return
		}
		for range p.size {
			p.wg.Add(1)
			go p.worker()
		}
	})
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-p.ctx.Done():
			return nil, nil, p.ctx.Err()
		case entry := <-p.ready:
			select {
			case <-entry.generation:
				_ = entry.conn.Close()
				continue
			default:
			}
			if p.ctx.Err() != nil || ctx.Err() != nil {
				_ = entry.conn.Close()
				continue
			}
			if time.Now().Before(entry.expires) {
				return entry.conn, entry.base, nil
			}
			_ = entry.conn.Close()
		}
	}
}

func (p *preconnectPool) worker() {
	defer p.wg.Done()
	for p.ctx.Err() == nil {
		p.mu.Lock()
		generation := p.generation
		p.mu.Unlock()
		dialCtx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
		conn, base, err := p.dial(dialCtx)
		cancel()
		if err == nil && conn != nil {
			timer := time.NewTimer(2 * time.Minute)
			select {
			case <-p.ctx.Done():
				_ = conn.Close()
			case <-generation:
				_ = conn.Close()
			case <-timer.C:
				_ = conn.Close()
			case p.ready <- preconnected{conn: conn, base: base, expires: time.Now().Add(2 * time.Minute), generation: generation}:
			}
			timer.Stop()
		} else if conn != nil {
			_ = conn.Close()
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-p.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *preconnectPool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	close(p.generation)
	p.generation = make(chan struct{})
}

func (p *preconnectPool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}
