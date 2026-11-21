package psiphon

import (
	"encoding/json"
	"sync"

	"github.com/Psiphon-Labs/psiphon-tunnel-core/psiphon"
)

// A single process-wide notice writer fans out to current instances; closing
// an old instance must not remove the writer that a replacement still needs.
// The process-level notice writer stays installed after all Psiphon instances close.
// Resetting it while holding access can deadlock a controller writing a notice.
var noticeHub struct {
	install   sync.Once
	installed error
	access    sync.Mutex
	listeners map[*Psiphon]func(NoticeEvent)
}

func noticeCount(data map[string]any) (float64, bool) {
	if data == nil {
		return 0, false
	}
	switch count := data["count"].(type) {
	case float64:
		return count, true
	case int:
		return float64(count), true
	case int64:
		return float64(count), true
	case json.Number:
		value, err := count.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

// installNoticeHub runs before opening the Psiphon datastore so notices cannot
// leak into the initial stderr pipe (notably on Windows after stdio detaches).
// Installation intentionally runs outside access: the tunnel core holds its
// writer lock when delivering notices and callbacks take access.
func installNoticeHub() error {
	noticeHub.install.Do(func() {
		// The core's default writer may still hold stderr from process startup.
		psiphon.ResetNoticeWriter()
		noticeHub.installed = psiphon.SetNoticeWriter(psiphon.NewNoticeReceiver(func(raw []byte) {
			var event NoticeEvent
			if json.Unmarshal(raw, &event) != nil {
				return
			}
			noticeHub.access.Lock()
			listeners := make([]func(NoticeEvent), 0, len(noticeHub.listeners))
			for _, listener := range noticeHub.listeners {
				listeners = append(listeners, listener)
			}
			noticeHub.access.Unlock()
			for _, listener := range listeners {
				listener(event)
			}
		}))
	})
	return noticeHub.installed
}

func listenNotices(instance *Psiphon, receiver func(NoticeEvent)) error {
	if err := installNoticeHub(); err != nil {
		return err
	}
	noticeHub.access.Lock()
	defer noticeHub.access.Unlock()
	if noticeHub.listeners == nil {
		noticeHub.listeners = make(map[*Psiphon]func(NoticeEvent))
	}
	noticeHub.listeners[instance] = receiver
	return nil
}

func stopListeningNotices(instance *Psiphon) {
	noticeHub.access.Lock()
	defer noticeHub.access.Unlock()
	delete(noticeHub.listeners, instance)
	// Keep the process-wide writer installed. ResetNoticeWriter here could
	// deadlock with a concurrent notice callback holding tunnel-core's lock.
}
