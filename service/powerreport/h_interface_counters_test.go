package powerreport

import "testing"

func TestH_InterfaceCounterDelta64Bit(t *testing.T) {
	cases := []struct {
		name     string
		current  uint64
		previous uint64
		want     uint64
	}{
		{name: "normal", current: 400, previous: 250, want: 150},
		{name: "more_than_32_bits", current: 1<<40 + 500, previous: 1<<32 + 200, want: (1<<40 + 500) - (1<<32 + 200)},
		{name: "interface_reset", current: 25, previous: 800, want: 0},
		{name: "unchanged", current: 128, previous: 128, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hInterfaceCounterDelta(tc.current, tc.previous); got != tc.want {
				t.Fatalf("hInterfaceCounterDelta(%d, %d) = %d, want %d", tc.current, tc.previous, got, tc.want)
			}
		})
	}
}
