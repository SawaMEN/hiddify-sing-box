package urltest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Zero denotes an unmeasured history, including when a successful probe is very fast.
func TestH_URLTestSubmillisecondSuccess(t *testing.T) {
	for _, mode := range []string{"regular", "unified"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			defer server.Close()
			dialer := &loopbackDialer{addr: server.Listener.Addr().String()}
			ctx := t.Context()
			if mode == "unified" {
				ctx = ContextWithIsUnifiedDelay(ctx)
			}
			for range 32 {
				delay, err := URLTest(ctx, server.URL, dialer)
				require.NoError(t, err)
				require.GreaterOrEqual(t, delay, uint16(1), "successful probes must be distinguishable from unmeasured")
			}
		})
	}
}
