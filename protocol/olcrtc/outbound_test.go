package olcrtc

import (
	"context"
	"errors"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"net"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/openlibrecommunity/olcrtc/mobile"
	"github.com/sagernet/sing-box/option"
)

const fixtureKey = "d823fa01cb3e0609b67322f7cf984c4ee2e4ce2e294936fc24ef38c9e59f4799"

func TestValidOlcRtcOutbound(t *testing.T) {
	for _, tc := range []struct{ provider, transport string }{
		{"jitsi", "datachannel"}, {"jitsi", "vp8channel"},
		{"wbstream", "vp8channel"}, {"wbstream", "seichannel"},
		{"telemost", "vp8channel"}, {"telemost", "videochannel"},
	} {
		if err := validate(option.OlcRTCOutboundOptions{
			Provider: tc.provider, Transport: tc.transport, RoomID: "room", Key: fixtureKey,
		}); err != nil {
			t.Fatalf("valid combination %s/%s rejected: %v", tc.provider, tc.transport, err)
		}
	}
}

func TestInvalidOlcRtcOutbound(t *testing.T) {
	for _, o := range []option.OlcRTCOutboundOptions{
		{Provider: "jazz", Transport: "datachannel", RoomID: "room", Key: fixtureKey},
		{Provider: "jitsi", Transport: "other", RoomID: "room", Key: fixtureKey},
		{Provider: "jitsi", Transport: "datachannel", RoomID: "", Key: fixtureKey},
		{Provider: "jitsi", Transport: "datachannel", RoomID: "room", Key: "private-secret"},
		{Provider: "telemost", Transport: "datachannel", RoomID: "room", Key: fixtureKey},
		{Provider: "telemost", Transport: "seichannel", RoomID: "room", Key: fixtureKey},
		{Provider: "wbstream", Transport: "datachannel", RoomID: "room", Key: fixtureKey},
		{Provider: "none", Transport: "datachannel", RoomID: "room", Key: fixtureKey},
		{Provider: "jitsi", Transport: "datachannel", RoomID: "room", Key: fixtureKey, DNSServer: "localhost:invalid"},
	} {
		err := validate(o)
		if err == nil {
			t.Fatal("accepted invalid configuration")
		}
		if strings.Contains(err.Error(), o.Key) {
			t.Fatal("credential leaked in error")
		}
	}
}

func TestOlcRTCConstructorDefaultsAndPrivateLoopbackAuth(t *testing.T) {
	for _, transport := range []string{"datachannel", "vp8channel", "seichannel", "videochannel"} {
		result, err := NewOutbound(context.Background(), nil, nil, "olcrtc-test", option.OlcRTCOutboundOptions{
			Provider: "jitsi", Transport: transport, RoomID: "https://meet.jit.si/test-room", Key: fixtureKey,
		})
		if err != nil {
			t.Fatalf("%s constructor failed: %v", transport, err)
		}
		outbound, ok := result.(*Outbound)
		if !ok || len(outbound.username) != 32 || len(outbound.password) != 32 || outbound.username == outbound.password {
			t.Fatalf("%s did not generate isolated SOCKS credentials", transport)
		}
		if len(outbound.Network()) != 1 || outbound.Network()[0] != "tcp" {
			t.Fatal("unsupported UDP advertised")
		}
		if outbound.runtime.IsRunning() {
			t.Fatalf("%s constructor unexpectedly started WebRTC traffic", transport)
		}
		if err := outbound.Close(); err != nil {
			t.Fatalf("close %s: %v", transport, err)
		}
		if err := outbound.Close(); err != nil {
			t.Fatalf("idempotent close %s: %v", transport, err)
		}
	}
}

// No signaling server is needed to exercise concurrency and cancellation.
type fakeRuntime struct {
	active  bool
	starts  int
	waits   atomic.Int32
	release chan struct{}
}

func (f *fakeRuntime) IsRunning() bool        { return f.active }
func (f *fakeRuntime) SetSocksPort(int) error { return nil }
func (f *fakeRuntime) Start() error           { f.active = true; f.starts++; return nil }
func (f *fakeRuntime) WaitReady(int) error {
	f.waits.Add(1)
	<-f.release
	return nil
}
func (f *fakeRuntime) Stop(int) error { close(f.release); return nil }

func lifecycleFixture() (*Outbound, *fakeRuntime) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &fakeRuntime{release: make(chan struct{})}
	return &Outbound{runtime: runtime, context: ctx, cancel: cancel}, runtime
}

func TestLifecycleIsLazyAndRejectsUDPWithoutStarting(t *testing.T) {
	h, runtime := lifecycleFixture()
	defer h.Close()
	if err := h.Start(adapter.StartStateStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DialContext(context.Background(), "udp", M.ParseSocksaddr("1.1.1.1:53")); err == nil {
		t.Fatal("UDP accepted")
	}
	if _, err := h.ListenPacket(context.Background(), M.ParseSocksaddr("1.1.1.1:53")); err == nil {
		t.Fatal("UDP listener accepted")
	}
	if runtime.starts != 0 {
		t.Fatal("unused or unsupported traffic started signaling")
	}
}

func TestCanceledRequestDoesNotStartRuntime(t *testing.T) {
	h, runtime := lifecycleFixture()
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if runtime.starts != 0 {
		t.Fatal("canceled request started signaling")
	}
}

func TestConcurrentWaitersShareReadinessAndCloseUnblocksThem(t *testing.T) {
	h, runtime := lifecycleFixture()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.ensure(context.Background())
			if !errors.Is(err, net.ErrClosed) {
				t.Errorf("want closed, got %v", err)
			}
		}()
	}
	deadline := time.After(time.Second)
	for runtime.waits.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("runtime never started")
		default:
			goruntime.Gosched()
		}
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if runtime.starts != 1 || runtime.waits.Load() != 1 {
		t.Fatal("readiness was not shared")
	}
	if _, err := h.ensure(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed outbound restarted: %v", err)
	}
}

func TestNegativeTransportOptionsAreRejected(t *testing.T) {
	options := option.OlcRTCOutboundOptions{Provider: "jitsi", Transport: "vp8channel", RoomID: "room", Key: fixtureKey, VP8FPS: -1}
	if _, err := NewOutbound(context.Background(), nil, nil, "test", options); err == nil {
		t.Fatal("negative FPS silently defaulted")
	}
}

type delayedRuntime struct{ fakeRuntime }

func (f *delayedRuntime) WaitReady(int) error {
	if f.waits.Add(1) == 1 {
		return upstream.ErrReadyTimeout
	}
	return nil
}

func TestReadinessTimeoutDoesNotPoisonLaterRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &delayedRuntime{fakeRuntime: fakeRuntime{release: make(chan struct{})}}
	h := &Outbound{runtime: runtime, context: ctx, cancel: cancel}
	defer h.Close()
	if _, err := h.ensure(ctx); !errors.Is(err, upstream.ErrReadyTimeout) {
		t.Fatalf("want initial timeout, got %v", err)
	}
	if client, err := h.ensure(ctx); err != nil || client == nil {
		t.Fatalf("did not recover after slow readiness: %v", err)
	}
	if runtime.starts != 1 || runtime.waits.Load() != 2 {
		t.Fatal("slow readiness restarted transport")
	}
}
