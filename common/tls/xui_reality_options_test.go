//go:build with_utls

package tls

import (
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestXUIPanelRealityClientVersionValidation(t *testing.T) {
	for _, value := range []struct {
		text string
		want []byte
		ok   bool
	}{
		{text: "", ok: true},
		{text: "26.10.31", want: []byte{26, 10, 31}, ok: true},
		{text: "0.0.0", want: []byte{0, 0, 0}, ok: true},
		{text: "256.1.1"},
		{text: "1.2"},
		{text: "1.2.3.4"},
		{text: "1.-1.2"},
		{text: "1.abc.2"},
	} {
		got, err := parseRealityClientVersion(value.text)
		if (err == nil) != value.ok || (value.ok && !reflect.DeepEqual(got, value.want)) {
			t.Fatalf("version %q => %v, %v; want %v, valid=%v", value.text, got, err, value.want, value.ok)
		}
	}
}

func TestXUIPanelRealityFallbackLimitValidation(t *testing.T) {
	got, err := parseRealityFallbackLimit(option.RealityLimitFallback{AfterBytes: 100, BytesPerSec: 1000, BurstBytesPerSec: 4000})
	if err != nil || got.AfterBytes != 100 || got.BytesPerSec != 1000 || got.BurstBytesPerSec != 4000 {
		t.Fatalf("valid fallback limit lost: %+v, %v", got, err)
	}
	for _, invalid := range []option.RealityLimitFallback{
		{AfterBytes: -1},
		{BytesPerSec: -1},
		{BurstBytesPerSec: -1},
	} {
		if _, err := parseRealityFallbackLimit(invalid); err == nil {
			t.Fatalf("negative fallback value accepted: %+v", invalid)
		}
	}
}
