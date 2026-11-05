package wireguard

import "context"

// Keep the existing test managers compatible with the context-aware pause API.
func (m *testPauseManager) WaitActiveContext(ctx context.Context) {
	m.waitingOnce.Do(func() { close(m.waiting) })
	select {
	case <-m.active:
	case <-ctx.Done():
	}
}

func (m *endpointPauseManager) WaitActiveContext(context.Context) {
	m.WaitActive()
}
