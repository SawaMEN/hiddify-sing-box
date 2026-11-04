//go:build linux

package oomprofile

import (
    "bufio"
    "bytes"
    "os"
    "strconv"
    "strings"
)

// parseProcSelfMaps avoids linking to the private runtime/pprof implementation,
// which changed across supported Go toolchains. We record only executable
// mappings, just like pprof's executable section index.
func parseProcSelfMaps(data []byte, addMapping func(lo, hi, offset uint64, file, buildID string)) {
    scanner := bufio.NewScanner(bytes.NewReader(data))
    for scanner.Scan() {
        fields := strings.Fields(scanner.Text())
        if len(fields) < 5 || !strings.Contains(fields[1], "x") {
            continue
        }
        bounds := strings.SplitN(fields[0], "-", 2)
        if len(bounds) != 2 {
            continue
        }
        lo, errLo := strconv.ParseUint(bounds[0], 16, 64)
        hi, errHi := strconv.ParseUint(bounds[1], 16, 64)
        offset, errOffset := strconv.ParseUint(fields[2], 16, 64)
        if errLo != nil || errHi != nil || errOffset != nil || lo >= hi {
            continue
        }
        file := ""
        if len(fields) > 5 {
            file = strings.Join(fields[5:], " ")
        }
        addMapping(lo, hi, offset, file, "")
    }
}

func (b *profileBuilder) readMapping() {
    data, err := os.ReadFile("/proc/self/maps")
    if err == nil {
        parseProcSelfMaps(data, func(lo, hi, offset uint64, file, buildID string) {
            b.addMappingEntry(lo, hi, offset, file, buildID, false)
        })
    }
    if len(b.mem) == 0 {
        b.addMappingEntry(0, 0, 0, "", "", true)
    }
}
