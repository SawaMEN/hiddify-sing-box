//go:build linux

package oomprofile

import "testing"

func TestParseProcSelfMaps(t *testing.T) {
    data := []byte("00400000-00452000 r-xp 00001000 08:01 1234 /usr/bin/core\n" +
        "00652000-00653000 r--p 00052000 08:01 1234 /usr/bin/core\n" +
        "7fffd000-7fffe000 r-xp 00000000 00:00 0 [vdso]\n" +
        "not-a-map\n")
    var got []string
    parseProcSelfMaps(data, func(lo, hi, off uint64, file, _ string) {
        if lo >= hi { t.Fatal("bad mapping") }
        if off == 0x1000 && lo != 0x400000 { t.Fatal("bad offset") }
        got = append(got, file)
    })
    if len(got) != 2 || got[0] != "/usr/bin/core" || got[1] != "[vdso]" {
        t.Fatalf("mappings = %v", got)
    }
}
