package peerfallback

import (
	"testing"
	"time"
)

func TestH_TimingIsCapturedAtConstruction(t *testing.T) {
	oldWait, oldInterval := Wait, Interval
	t.Cleanup(func() { Wait, Interval = oldWait, oldInterval })
	Wait, Interval = 0, time.Millisecond
	fallback, state := newFake()
	Wait, Interval = oldWait, oldInterval
	state.tx = 148
	fallback.Check()
	if len(state.moves) != 1 {
		t.Fatal("fallback must keep its own wait duration")
	}
	if fallback.interval != time.Millisecond {
		t.Fatal("fallback must keep its own interval")
	}
}
