package monitoring

import (
	"context"
	"testing"
)

func TestFullQueueDoesNotSuppressFutureTests(t *testing.T) {
	state := &outboundState{}
	monitor := &OutboundMonitoring{ctx: context.Background(), normalQueue: make(chan *testTask, 1)}
	registry := newMonitoringRegistry()
	registry.outbounds["server"] = state
	monitor.registry.Store(registry)
	monitor.normalQueue <- &testTask{}
	task := &testTask{outboundTag: "server", cycleID: 1}
	if monitor.enqueueTask(task) {
		t.Fatal("full queue accepted task")
	}
	<-monitor.normalQueue
	if !monitor.enqueueTask(task) {
		t.Fatal("dropped task permanently marked queued")
	}
}

func TestFullQueuePreservesPreviouslyQueuedCycle(t *testing.T) {
	state := &outboundState{enqueuedCycle: 1, queued: true}
	monitor := &OutboundMonitoring{ctx: context.Background(), normalQueue: make(chan *testTask, 1)}
	registry := newMonitoringRegistry()
	registry.outbounds["server"] = state
	monitor.registry.Store(registry)
	monitor.normalQueue <- &testTask{cycleID: 1}
	if monitor.enqueueTask(&testTask{outboundTag: "server", cycleID: 2}) {
		t.Fatal("full queue accepted new cycle")
	}
	if state.enqueuedCycle != 1 || !state.queued {
		t.Fatal("failed enqueue erased the pending old cycle")
	}
}
