package vless

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	XB "github.com/xtls/xray-core/common/buf"
	XT "github.com/xtls/xray-core/transport"
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

func TestXUIReversePacketRemoteEOFIsPersistent(t *testing.T) {
	reader := XB.NewReader(bytes.NewReader(nil))
	writer := XB.NewWriter(io.Discard)
	packet := newReversePacket(&XT.Link{Reader: reader, Writer: writer}, M.Socksaddr{})
	defer packet.Close()

	for range 2 {
		if err := packet.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		n, _, err := packet.ReadFrom(make([]byte, 8))
		if n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("remote EOF lost or blocked: n=%d err=%v", n, err)
		}
	}
}
