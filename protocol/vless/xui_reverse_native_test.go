package vless

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

func TestXUIReversePortalRejectsMissingTag(t *testing.T) {
	portal, err := NewReversePortal(context.Background(), nil, nil, "", option.VLESSReversePortalOptions{})
	if err == nil || portal != nil {
		t.Fatalf("invalid unnamed portal constructed: %v", err)
	}
}

func TestXUIReversePortalFailsClosedWithoutBridge(t *testing.T) {
	value, err := NewReversePortal(context.Background(), nil, nil, "portal", option.VLESSReversePortalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	portal := value.(*reversePortal)
	if _, err := portal.PickAvailable(); err == nil || !strings.Contains(err.Error(), "no connected bridge") {
		t.Fatalf("portal without authenticated bridge accepted a worker: %v", err)
	}
	destination := M.ParseSocksaddrHostPort("example.com", 443)
	for _, network := range []string{"tcp", "udp"} {
		if network == "tcp" {
			if c, err := portal.DialContext(context.Background(), network, destination); err == nil {
				_ = c.Close()
				t.Fatalf("unauthenticated reverse %s dial succeeded", network)
			}
		} else {
			if c, err := portal.ListenPacket(context.Background(), destination); err == nil {
				_ = c.Close()
				t.Fatal("unauthenticated reverse UDP listener succeeded")
			}
		}
	}
	if err := portal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := portal.PickAvailable(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed portal unexpectedly available: %v", err)
	}
	if err := portal.Close(); err != nil {
		t.Fatal(err)
	}
}
