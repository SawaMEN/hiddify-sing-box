package tls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResolveCipherSuitesPreservesExplicitLegacy(t *testing.T) {
	const modern = "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"
	const rsa = "TLS_RSA_WITH_AES_128_GCM_SHA256"
	ids, legacy, err := resolveCipherSuites([]string{modern, rsa, modern, "TLS_AES_128_GCM_SHA256"})
	if err != nil {
		t.Fatal(err)
	}
	want := []uint16{
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_AES_128_GCM_SHA256,
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("cipher suite IDs = %#v, want %#v", ids, want)
	}
	if !reflect.DeepEqual(legacy, []string{rsa}) {
		t.Fatalf("legacy warning list = %#v", legacy)
	}
}

func TestResolveCipherSuitesLegacyOnly(t *testing.T) {
	names := []string{
		"TLS_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_RSA_WITH_AES_128_CBC_SHA256",
		"TLS_RSA_WITH_AES_256_CBC_SHA",
		"TLS_RSA_WITH_3DES_EDE_CBC_SHA",
		"TLS_RSA_WITH_RC4_128_SHA",
	}
	ids, legacy, err := resolveCipherSuites(names)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(names) || !reflect.DeepEqual(legacy, names) {
		t.Fatalf("explicit legacy ciphers lost: ids=%#v warnings=%#v", ids, legacy)
	}
}

func TestResolveCipherSuitesRejectsUnknown(t *testing.T) {
	_, _, err := resolveCipherSuites([]string{"TLS_NOT_SUPPORTED"})
	if err == nil || !strings.Contains(err.Error(), "unknown cipher_suite") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Exercise the actual Go TLS handshake, not just the parser: Go does not offer
// RSA key exchange by default, but it does negotiate it when selected explicitly
// on both ends of a TLS 1.2 connection.
func TestExplicitLegacyRSACipherNegotiatesTLS12(t *testing.T) {
	ids, _, err := resolveCipherSuites([]string{"TLS_RSA_WITH_AES_128_GCM_SHA256"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()
	deadline := time.Now().Add(5 * time.Second)
	_ = srv.SetDeadline(deadline)
	_ = cli.SetDeadline(deadline)
	server := tls.Server(srv, &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: key}},
		CipherSuites: ids,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
	})
	client := tls.Client(cli, &tls.Config{
		ServerName: "localhost",
		InsecureSkipVerify: true, // Test-only certificate: the cipher is under test.
		CipherSuites: ids,
		MinVersion: tls.VersionTLS12,
		MaxVersion: tls.VersionTLS12,
	})
	serverResult := make(chan error, 1)
	go func() { serverResult <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	if got := client.ConnectionState().CipherSuite; got != tls.TLS_RSA_WITH_AES_128_GCM_SHA256 {
		t.Fatalf("negotiated suite = 0x%x", got)
	}
}
