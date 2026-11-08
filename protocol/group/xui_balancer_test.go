package group

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

func TestXUIPanelDynamicBalancing(t *testing.T) {
	targets := []string{"a", "b"}
	for _, kind := range []string{"random", "roundRobin", "leastPing", "leastLoad"} {
		t.Run(kind, func(t *testing.T) {
			options := option.XrayBalancerOptions{Strategy: kind, Fallback: "backup", ProbeURL: "http://127.0.0.1:1/probe"}
			state, err := newBalanceState(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			state.record("a", 10_000_000, nil)
			state.record("b", 100_000_000, nil)
			chosen := map[string]int{}
			for i := 0; i < 100; i++ {
				tag, err := state.pick(targets)
				if err != nil {
					t.Fatal(err)
				}
				chosen[tag]++
			}
			if kind == "random" || kind == "roundRobin" {
				if chosen["a"] == 0 || chosen["b"] == 0 {
					t.Fatal("dynamic selection stayed static")
				}
			} else {
				if chosen["a"] != 100 {
					t.Fatalf("fast node not selected: %v", chosen)
				}
			}
			state.record("a", 0, fmt.Errorf("offline"))
			state.record("b", 0, fmt.Errorf("offline"))
			tag, err := state.pick(targets)
			if err != nil || tag != "backup" {
				t.Fatalf("fallback: %s %v", tag, err)
			}
		})
	}
}

// Probes use the selected outbound's dialer, never the system route.
func TestXUIPanelBalancerProbeAndFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("probe method %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	state, err := newBalanceState(context.Background(), option.XrayBalancerOptions{Strategy: "leastPing", Fallback: "backup", ProbeURL: server.URL, Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	fast := &probeOutbound{Adapter: outbound.NewAdapter("direct", "fast", nil, nil)}
	dead := &probeOutbound{Adapter: outbound.NewAdapter("direct", "dead", nil, nil), fail: true}
	state.targets = map[string]adapter.Outbound{"fast": fast, "dead": dead}
	state.probe()
	tag, err := state.pick([]string{"fast", "dead"})
	if err != nil || tag != "fast" {
		t.Fatalf("probe choice: %s %v", tag, err)
	}
	if fast.calls.Load() != 1 || dead.calls.Load() != 1 {
		t.Fatal("probes bypassed outbound dialers")
	}
	fast.fail = true
	state.probe()
	tag, err = state.pick([]string{"fast", "dead"})
	if err != nil || tag != "backup" {
		t.Fatalf("probe fallback: %s %v", tag, err)
	}
}

type probeOutbound struct {
	outbound.Adapter
	fail  bool
	calls atomic.Int32
}

func (p *probeOutbound) DialContext(ctx context.Context, network string, address M.Socksaddr) (net.Conn, error) {
	p.calls.Add(1)
	if p.fail {
		return nil, fmt.Errorf("offline")
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address.String())
}

func (p *probeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("unused")
}

func TestXUIPanelBalancerCloseBeforeStart(t *testing.T) {
	state, err := newBalanceState(context.Background(), option.XrayBalancerOptions{Strategy: "random", ProbeURL: "http://127.0.0.1:1/"})
	if err != nil {
		t.Fatal(err)
	}
	state.Close()
	target := &probeOutbound{Adapter: outbound.NewAdapter("direct", "never", nil, nil), fail: true}
	state.postStart(map[string]adapter.Outbound{"never": target})
	state.Close()
	if target.calls.Load() != 0 {
		t.Fatal("probe after shutdown")
	}
}

func TestXUIPanelBalancerRejectsNullCost(t *testing.T) {
	_, err := newBalanceState(context.Background(), option.XrayBalancerOptions{Strategy: "leastLoad", ProbeURL: "http://127.0.0.1/probe", Settings: []byte(`{"costs":[null]}`)})
	if err == nil {
		t.Fatal("null cost accepted")
	}
}
