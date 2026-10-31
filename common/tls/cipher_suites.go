package tls

import (
	"crypto/tls"
	"fmt"
	"strings"
)

// resolveCipherSuites resolves an explicit TLS cipher-suite list to the IDs
// accepted by Go's crypto/tls. The standard tls.CipherSuites enumeration excludes
// legacy RSA key-exchange, CBC and 3DES suites, even though crypto/tls can still
// negotiate them when explicitly selected via tls.Config.CipherSuites.
//
// Never enable these legacy suites implicitly: a nil cipher_suites list keeps
// Go's secure defaults. The operator's explicit list is preserved in order,
// including legacy entries, and unknown names still fail fast.
func resolveCipherSuites(names []string) (ids []uint16, legacy []string, err error) {
	known := make(map[string]uint16)
	unsafe := make(map[string]bool)
	for _, suite := range tls.CipherSuites() {
		known[suite.Name] = suite.ID
	}
	for _, suite := range tls.InsecureCipherSuites() {
		known[suite.Name] = suite.ID
		unsafe[suite.Name] = true
	}
	seen := make(map[uint16]bool)
	for _, name := range names {
		name = strings.TrimSpace(name)
		id, found := known[name]
		if !found {
			return nil, nil, fmt.Errorf("unknown cipher_suite: %s", name)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if unsafe[name] {
			legacy = append(legacy, name)
		}
	}
	return ids, legacy, nil
}
