//go:build with_utls

package tls

import (
	"context"
	"crypto/tls"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

func TestXUIPanelExplicitCipherSuites(t *testing.T) {
	modern := "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	legacy := "TLS_RSA_WITH_AES_128_GCM_SHA256"
	for _, tc := range []struct {
		name   string
		names  []string
		want   []uint16
		unsafe []string
		bad    bool
	}{
		{name: "default"},
		{name: "empty", names: []string{}},
		{name: "modern", names: []string{modern}, want: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}},
		{name: "explicit legacy", names: []string{legacy}, want: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256}, unsafe: []string{legacy}},
		{name: "mixed order with duplicate", names: []string{legacy, modern, legacy}, want: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}, unsafe: []string{legacy}},
		{name: "unknown after valid", names: []string{modern, "TLS_AUDIT_UNKNOWN"}, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, unsafe, err := resolveCipherSuites(tc.names)
			if (err != nil) != tc.bad {
				t.Fatalf("cipher lookup error = %v, expected bad=%v", err, tc.bad)
			}
			if !tc.bad && (!reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(unsafe, tc.unsafe)) {
				t.Fatalf("resolved ciphers = %v, unsafe %v; expected %v, %v", got, unsafe, tc.want, tc.unsafe)
			}
		})
	}
	for _, list := range [][]*tls.CipherSuite{tls.CipherSuites(), tls.InsecureCipherSuites()} {
		for _, suite := range list {
			got, _, err := resolveCipherSuites([]string{suite.Name})
			if err != nil || len(got) != 1 || got[0] != suite.ID {
				t.Fatalf("TLS suite %s lost: %v %v", suite.Name, got, err)
			}
		}
	}
}

func TestXUIPanelExplicitCipherClientConstructors(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"TLS_RSA_WITH_AES_128_GCM_SHA256"}, {"TLS_AUDIT_UNKNOWN"}} {
		options := option.OutboundTLSOptions{Enabled: true, ServerName: "example.invalid", CipherSuites: names}
		standard, err := NewSTDClient(context.Background(), logger.NOP(), "example.invalid", options)
		want, _, lookupErr := resolveCipherSuites(names)
		if (err != nil) != (lookupErr != nil) {
			t.Fatalf("STD client cipher acceptance differs: %v", err)
		}
		if err == nil {
			config, configErr := standard.STDConfig()
			if configErr != nil || !reflect.DeepEqual(config.CipherSuites, want) {
				t.Fatalf("STD client lost explicit policy: %v, %v", config, configErr)
			}
		}
		options.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"}
		fingerprinted, err := NewUTLSClient(context.Background(), logger.NOP(), "example.invalid", options)
		if (err != nil) != (lookupErr != nil) {
			t.Fatalf("uTLS client cipher acceptance differs: %v", err)
		}
		if err == nil && !reflect.DeepEqual(fingerprinted.(*UTLSClientConfig).config.CipherSuites, want) {
			t.Fatal("uTLS client lost explicit cipher policy")
		}
	}
}
