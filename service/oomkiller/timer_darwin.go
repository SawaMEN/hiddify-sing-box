//go:build darwin && cgo

package oomkiller

// notifyPressure accelerates the next sample after an OS memory-pressure event.
func (t *adaptiveTimer) notifyPressure() {
	t.access.Lock()
	defer t.access.Unlock()
	if t.timer == nil {
		return
	}
	t.forceMinInterval = true
	t.pendingPressureBaseline = true
	t.timer.Reset(t.minInterval)
}
