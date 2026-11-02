package psiphon

import (
	"encoding/json"
	"sync"

	"github.com/Psiphon-Labs/psiphon-tunnel-core/psiphon"
)

// A single process-wide notice writer fans out to current instances; closing
// an old instance must not remove the writer that a replacement still needs.
var noticeHub struct {
	access    sync.Mutex
	installed bool
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

func listenNotices(instance *Psiphon, receiver func(NoticeEvent)) error {
	noticeHub.access.Lock()
	defer noticeHub.access.Unlock()
	if noticeHub.listeners == nil {
		noticeHub.listeners = make(map[*Psiphon]func(NoticeEvent))
	}
	if !noticeHub.installed {
		psiphon.SetNoticeWriter(psiphon.NewNoticeReceiver(func(raw []byte) {
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
		noticeHub.installed = true
	}
	noticeHub.listeners[instance] = receiver
	return nil
}

func stopListeningNotices(instance *Psiphon) {
	noticeHub.access.Lock()
	defer noticeHub.access.Unlock()
	delete(noticeHub.listeners, instance)
	if noticeHub.installed && len(noticeHub.listeners) == 0 {
		psiphon.ResetNoticeWriter()
		noticeHub.installed = false
	}
}
