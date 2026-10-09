package box

import (
	"github.com/sagernet/sing-box/log"
	"strings"
	"testing"
	"time"
)

func TestCloseWorkerPanicBecomesError(t *testing.T) {
	box := &Box{logger: log.NewNOPFactory().Logger()}
	err := box.closeWithTimeout("test-service", time.Second, func() error { panic("broken close") })
	if err == nil || !strings.Contains(err.Error(), "broken close") {
		t.Fatalf("panic not returned: %v", err)
	}
	// A failed worker must not damage the next close operation.
	if err := box.closeWithTimeout("next", time.Second, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
