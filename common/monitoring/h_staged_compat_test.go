package monitoring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing/common/json/badoption"
)

func TestH_OnDemandTimeouts(t *testing.T) {
	defaults := onDemandTimeouts(nil)
	if len(defaults) != 3 || defaults[0] != time.Second || defaults[1] != 3*time.Second || defaults[2] != 10*time.Second {
		t.Fatalf("invalid staged retry defaults: %v", defaults)
	}
	custom := onDemandTimeouts(badoption.Listable[badoption.Duration]{
		badoption.Duration(time.Second),
		badoption.Duration(time.Second),
		badoption.Duration(3 * time.Second),
		badoption.Duration(2 * time.Second),
	})
	if len(custom) != 2 || custom[0] != time.Second || custom[1] != 3*time.Second {
		t.Fatalf("non-increasing retry deadlines must be discarded: %v", custom)
	}
}

func TestH_StagedURLTestBoundedFailure(t *testing.T) {
	m := &OutboundMonitoring{onDemand: []time.Duration{20 * time.Millisecond, 100 * time.Millisecond}}
	out := &fakeOutbound{tag: "unreachable"}
	start := time.Now()
	delay, err := m.stagedURLTest(context.Background(), "https://example.invalid/generate_204", out, nil, false)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) && delay != TimeoutDelay {
		t.Fatalf("unreachable outbound should fail: delay=%d, err=%v", delay, err)
	}
	if elapsed := time.Since(start); elapsed > 800*time.Millisecond {
		t.Fatalf("staged retries exceeded their deadline: %s", elapsed)
	}
}
