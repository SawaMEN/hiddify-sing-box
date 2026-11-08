package kcp

import (
	"sync"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

// Read keeps what does not fit in leftOver; Release, from Terminate, frees it from another
// goroutine. Run with -race.
func TestH_ReceivingWorkerReadRelease(t *testing.T) {
	var group sync.WaitGroup
	for range 100 {
		worker := &ReceivingWorker{window: NewReceivingWindow()}
		chunk := buf.New()
		chunk.Extend(64)
		worker.Lock()
		worker.leftOver = buf.MultiBuffer{chunk}
		worker.Unlock()
		group.Add(2)
		go func() {
			defer group.Done()
			worker.Read(make([]byte, 16))
		}()
		go func() {
			defer group.Done()
			worker.Release()
		}()
		group.Wait()
	}
}

func TestXUIPanelKCPReleaseCleansWindowAndRejectsLateSegments(t *testing.T) {
	worker := &ReceivingWorker{
		window:     NewReceivingWindow(),
		windowSize: 8,
		acklist:    &AckList{},
	}
	pending := NewDataSegment()
	pending.Number = 1
	pending.Data().Write([]byte("buffered"))
	worker.ProcessSegment(pending)
	if len(worker.window.cache) != 1 {
		t.Fatal("out-of-order segment was not buffered")
	}
	worker.Release()
	worker.Release() // Termination is idempotent.
	if len(worker.window.cache) != 0 || pending.payload != nil {
		t.Fatal("KCP release retained an out-of-order segment")
	}
	late := NewDataSegment()
	late.Data().Write([]byte("late"))
	worker.ProcessSegment(late)
	if late.payload != nil || len(worker.window.cache) != 0 {
		t.Fatal("a terminated receiver retained a late segment")
	}
	if worker.Read(make([]byte, 32)) != 0 || !worker.ReadMultiBuffer().IsEmpty() || worker.IsDataAvailable() || worker.UpdateNecessary() {
		t.Fatal("terminated receiver exposed buffered data")
	}
}

func TestXUIPanelKCPOutOfWindowSegmentIsReleased(t *testing.T) {
	worker := &ReceivingWorker{window: NewReceivingWindow(), windowSize: 1, acklist: &AckList{}}
	segment := NewDataSegment()
	segment.Number = 5
	segment.Data().Write([]byte("too far ahead"))
	worker.ProcessSegment(segment)
	if segment.payload != nil || len(worker.window.cache) != 0 {
		t.Fatal("out-of-window segment was not released")
	}
	worker.Release()
}
