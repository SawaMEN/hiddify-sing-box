package monitoring

import (
	"context"
	"testing"
)

func TestFullQueueDoesNotSuppressFutureTests(t *testing.T) {
	for _, priority := range []bool{false, true} {
		state := &outboundState{}
		monitor := &OutboundMonitoring{ctx: context.Background(), priorityQueue: make(chan *testTask, 1), normalQueue: make(chan *testTask, 1)}
		registry := newMonitoringRegistry()
		registry.outbounds["server"] = state
		monitor.registry.Store(registry)
		queue := monitor.normalQueue
		if priority {
			queue = monitor.priorityQueue
		}
		queue <- &testTask{}
		task := &testTask{outboundTag: "server", priority: priority, cycleID: 1}
		if monitor.enqueueTask(task) {
			t.Fatal("full queue accepted task")
		}
		<-queue
		if !monitor.enqueueTask(task) {
			t.Fatalf("priority=%v: dropped task permanently marked queued", priority)
		}
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
