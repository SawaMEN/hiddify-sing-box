package option

import (
    "net/netip"
    "testing"
)

func TestRay2singBackwardCompatibleOptionFields(t *testing.T) {
    peer := WireGuardPeer{AllowedIPs: LegacyListable[netip.Prefix]{netip.MustParsePrefix("10.0.0.0/8")}}
    if len(peer.AllowedIPs) != 1 {
        t.Fatal("WireGuard peer allowed IPs lost")
    }
    awg := AwgOptions{ContentPaddingAddition: AwgRange("5-10")}
    if awg.ContentPaddingAddition != "5-10" {
        t.Fatal("AWG range lost")
    }
    masque := MASQUEOutboundOptions{UniqueIdentifier: "device-a"}
    if masque.UniqueIdentifier != "device-a" {
        t.Fatal("MASQUE device identifier lost")
    }
}
