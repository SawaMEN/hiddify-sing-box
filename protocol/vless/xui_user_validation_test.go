package vless

import (
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestXUIPanelVLESSUserUUIDUniqueness(t *testing.T) {
	const id1 = "11111111-1111-4111-8111-111111111111"
	const id2 = "22222222-2222-4222-8222-222222222222"
	for name, tc := range map[string]struct {
		users   []option.VLESSUser
		reject  bool
	}{
		"empty":        {nil, false},
		"unique":       {[]option.VLESSUser{{UUID: id1}, {UUID: id2}}, false},
		"duplicate":    {[]option.VLESSUser{{UUID: id1}, {UUID: id1}}, true},
		"case-folded":  {[]option.VLESSUser{{UUID: id1}, {UUID: strings.ToUpper(id1)}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateVLESSUserUUIDs(tc.users)
			if (err != nil) != tc.reject {
				t.Fatalf("rejected=%v want=%v: %v", err != nil, tc.reject, err)
			}
		})
	}
}
