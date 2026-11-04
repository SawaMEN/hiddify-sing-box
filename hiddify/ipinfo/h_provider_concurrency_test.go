package ipinfo

import (
	C "github.com/sagernet/sing-box/constant"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestH_ConcurrentProviderDefaultUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != C.DefaultBrowserAgent {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"ip":"127.0.0.1"}`))
	}))
	defer server.Close()
	provider := &BaseProvider{URL: server.URL}
	var workers sync.WaitGroup
	errors := make(chan error, 32)
	for range 32 {
		workers.Go(func() { _, _, err := provider.fetchData(t.Context(), nil); errors <- err })
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Empty(t, provider.UserAgent)
}
