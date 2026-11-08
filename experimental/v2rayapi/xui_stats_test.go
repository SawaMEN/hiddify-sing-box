package v2rayapi

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
)

type xuiStatsOutbound struct {
	adapter.Outbound
	tag string
}

func (o *xuiStatsOutbound) Tag() string { return o.tag }

func TestXUIPanelStatsServiceNilIsNotTypedNil(t *testing.T) {
	server := &Server{}
	if got := server.StatsService(); got != nil {
		t.Fatalf("disabled stats returned non-nil interface: %T", got)
	}
}

func TestXUIPanelRoutedOutboundConnectionDirections(t *testing.T) {
	stats := NewStatsService(option.V2RayStatsServiceOptions{Enabled: true, Outbounds: []string{"foreign"}})
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if err := left.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := right.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	outbound := &xuiStatsOutbound{tag: "foreign"}
	wrapped := stats.RoutedOutboundConnection(context.Background(), left, outbound)
	result := make(chan error, 1)
	go func() {
		data := make([]byte, len("upload"))
		if _, err := io.ReadFull(right, data); err != nil {
			result <- err
			return
		}
		_, err := io.WriteString(right, "reply")
		result <- err
	}()
	if _, err := io.WriteString(wrapped, "upload"); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("reply"))
	if _, err := io.ReadFull(wrapped, data); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if string(data) != "reply" {
		t.Fatalf("unexpected reply: %q", data)
	}
	for _, tc := range []struct {
		name string
		want int64
	}{
		{"outbound>>>foreign>>>traffic>>>uplink", 6},
		{"outbound>>>foreign>>>traffic>>>downlink", 5},
	} {
		response, err := stats.GetStats(context.Background(), &GetStatsRequest{Name: tc.name})
		if err != nil {
			t.Fatal(err)
		}
		if response.Stat.Value != tc.want {
			t.Fatalf("%s: got %d want %d", tc.name, response.Stat.Value, tc.want)
		}
	}
}

func TestXUIPanelRoutedOutboundConnectionUnknownTag(t *testing.T) {
	stats := NewStatsService(option.V2RayStatsServiceOptions{Enabled: true, Outbounds: []string{"foreign"}})
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if got := stats.RoutedOutboundConnection(context.Background(), left, &xuiStatsOutbound{tag: "other"}); got != left {
		t.Fatal("untracked outbound unexpectedly wrapped")
	}
}
