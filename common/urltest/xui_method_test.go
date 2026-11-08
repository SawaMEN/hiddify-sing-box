package urltest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestXUIPanelURLTestWithMethod(t *testing.T) {
	hits := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	dialer := &loopbackDialer{addr: server.Listener.Addr().String()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		if _, err := URLTestWithMethod(ctx, server.URL, method, dialer); err != nil {
			t.Fatalf("%s probe failed: %v", method, err)
		}
		if got := <-hits; got != method {
			t.Fatalf("expected %s request, got %s", method, got)
		}
	}
	if _, err := URLTestWithMethod(ctx, server.URL, http.MethodGet, nil); err == nil {
		t.Fatal("nil dialer accepted")
	}
}
