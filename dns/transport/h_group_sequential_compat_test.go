package transport

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

func TestH_GroupSequentialFailureOrdering(t *testing.T) {
	s := newGroupSequential(option.GroupDNSServerOptions{
		ErrorTTL: badoption.Duration(time.Minute),
	})
	tags := []string{"primary", "backup", "last"}
	if got := s.order(tags); !slices.Equal(got, tags) {
		t.Fatalf("initial order: got %v, want %v", got, tags)
	}
	s.record("primary", groupFailed)
	if got := s.order(tags); !slices.Equal(got, []string{"backup", "last", "primary"}) {
		t.Fatalf("after a failure: unexpected order %v", got)
	}
	s.record("primary", groupAccepted)
	if got := s.order(tags); !slices.Equal(got, tags) {
		t.Fatalf("after recovery: unexpected order %v", got)
	}
	s.record("backup", groupFailed)
	s.reset()
	if got := s.order(tags); !slices.Equal(got, tags) {
		t.Fatalf("after reset: unexpected order %v", got)
	}
}

func TestH_GroupSequentialMemberTimeout(t *testing.T) {
	s := newGroupSequential(option.GroupDNSServerOptions{
		MemberTimeout: badoption.Duration(250 * time.Millisecond),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	budget := s.budget(ctx, 3)
	if budget <= 0 || budget > 100*time.Millisecond {
		t.Fatalf("timeout must respect request deadline, got %v", budget)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	budget = s.budget(ctx2, 3)
	if budget <= 0 || budget > 250*time.Millisecond {
		t.Fatalf("timeout must respect per-member limit, got %v", budget)
	}
}
