package transport

import (
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/transport/compat/openflux/transport/control"
)

type recordingExchanger struct {
	mu  sync.Mutex
	jar map[string]string
}

func (r *recordingExchanger) FetchCookies() (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.jar))
	maps.Copy(out, r.jar)
	return out, nil
}

func (r *recordingExchanger) ApplyCookies(jar map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jar = make(map[string]string, len(jar))
	maps.Copy(r.jar, jar)
	return nil
}

func (r *recordingExchanger) snapshot() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.jar))
	maps.Copy(out, r.jar)
	return out
}

// TestCookieExchangeEndToEnd wires two NegotiatedTransports and verifies that
// a SubtypeCookiesRequest from the client causes the exit to reply with its
// jar, which the client then applies.
func TestCookieExchangeEndToEnd(t *testing.T) {
	ca, cb, _, _ := sessionPair(t)
	clientEx := &recordingExchanger{}
	exitEx := &recordingExchanger{jar: map[string]string{"exit_cookie": "abc"}}

	// Replicate the wiring main.go does.
	wire := func(n *Session, isExit bool, exch CookieExchanger) {
		n.SetControlHandler(func(sub control.Subtype, payload []byte) {
			switch sub {
			case control.SubtypeCookiesRequest:
				if !isExit {
					return
				}
				jar, _ := exch.FetchCookies()
				body, _ := (&control.CookiesPayload{Jar: jar}).Encode()
				_ = n.SendControl(control.SubtypeCookiesResponse, body)
			case control.SubtypeCookiesResponse, control.SubtypeCookiesOffer:
				cp, err := control.DecodeCookies(payload)
				if err != nil || len(cp.Jar) == 0 {
					return
				}
				_ = exch.ApplyCookies(cp.Jar)
			}
		})
	}
	wire(ca, false, clientEx)
	wire(cb, true, exitEx)

	errs := make(chan error, 2)
	go func() { errs <- ca.Start() }()
	go func() { errs <- cb.Start() }()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	if err := ca.SendControl(control.SubtypeCookiesRequest, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if clientEx.snapshot()["exit_cookie"] == "abc" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("client did not receive exit cookies: %+v", clientEx.snapshot())
}
