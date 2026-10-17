package olcrtc

import (
    "strings"
    "context"
    "testing"

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
        {Provider:"jazz", Transport:"datachannel", RoomID:"room", Key:fixtureKey},
        {Provider:"jitsi", Transport:"other", RoomID:"room", Key:fixtureKey},
        {Provider:"jitsi", Transport:"datachannel", RoomID:"", Key:fixtureKey},
        {Provider:"jitsi", Transport:"datachannel", RoomID:"room", Key:"private-secret"},
        {Provider:"telemost", Transport:"datachannel", RoomID:"room", Key:fixtureKey},
        {Provider:"telemost", Transport:"seichannel", RoomID:"room", Key:fixtureKey},
        {Provider:"wbstream", Transport:"datachannel", RoomID:"room", Key:fixtureKey},
        {Provider:"none", Transport:"datachannel", RoomID:"room", Key:fixtureKey},
        {Provider:"jitsi", Transport:"datachannel", RoomID:"room", Key:fixtureKey, DNSServer:"localhost:invalid"},
    } {
        err := validate(o)
        if err == nil { t.Fatal("accepted invalid configuration") }
        if strings.Contains(err.Error(), o.Key) { t.Fatal("credential leaked in error") }
    }
}

func TestOlcRTCConstructorDefaultsAndPrivateLoopbackAuth(t *testing.T) {
    for _, transport := range []string{"datachannel", "vp8channel", "seichannel", "videochannel"} {
        result, err := NewOutbound(context.Background(), nil, nil, "olcrtc-test", option.OlcRTCOutboundOptions{
            Provider: "jitsi", Transport: transport, RoomID: "https://meet.jit.si/test-room", Key: fixtureKey,
        })
        if err != nil { t.Fatalf("%s constructor failed: %v", transport, err) }
        outbound, ok := result.(*Outbound)
        if !ok || len(outbound.username) != 32 || len(outbound.password) != 32 || outbound.username == outbound.password {
            t.Fatalf("%s did not generate isolated SOCKS credentials", transport)
        }
        if outbound.runtime.IsRunning() {
            t.Fatalf("%s constructor unexpectedly started WebRTC traffic", transport)
        }
        if err := outbound.Close(); err != nil { t.Fatalf("close %s: %v", transport, err) }
        if err := outbound.Close(); err != nil { t.Fatalf("idempotent close %s: %v", transport, err) }
    }
}
