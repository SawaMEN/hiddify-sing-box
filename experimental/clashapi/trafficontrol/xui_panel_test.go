package trafficontrol

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

func TestXUIPanelAuthenticatedUserIsConnectionMetadata(t *testing.T) {
	tracker := TrackerMetadata{
		Metadata: adapter.InboundContext{User: "alice"},
		Upload: new(atomic.Int64), Download: new(atomic.Int64),
	}
	data, err := tracker.MarshalJSON()
	if err != nil { t.Fatal(err) }
	var connection struct {
		User string `json:"user"`
		Metadata struct { User string `json:"user"` } `json:"metadata"`
	}
	if err := json.Unmarshal(data, &connection); err != nil { t.Fatal(err) }
	if connection.Metadata.User != "alice" || connection.User != "" {
		t.Fatalf("authenticated user is outside connection metadata: %s", data)
	}
}
