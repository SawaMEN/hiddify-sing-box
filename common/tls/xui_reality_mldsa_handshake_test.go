//go:build with_utls

package tls

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	utls "github.com/metacubex/utls"
)

// This is a wire-level test of the actual uTLS REALITY server and sing-box
// client handshake, not merely a standalone ML-DSA Sign/Verify round trip.
// An unrelated local TLS origin provides the plausible ServerHello that the
// REALITY server reuses as camouflage.
func TestXUIPanelRealityMLDSAAuthenticatedHandshake(t *testing.T) {
	// REALITY must fit its ML-DSA-65 signed certificate inside the
	// camouflage TLS origin's certificate record. The small default test
	// certificate is not large enough for a 3.3 KB ML-DSA signature, and
	// causes a negative padding error before verification can be tested.
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	originKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	originCert := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"example.com"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtraExtensions: []pkix.Extension{{
			Id:    []int{1, 2, 3, 4},
			Value: make([]byte, 6000),
		}},
	}
	originDER, err := x509.CreateCertificate(rand.Reader, &originCert, &originCert, &originKey.PublicKey, originKey)
	if err != nil {
		t.Fatal(err)
	}
	origin.TLS = &tls.Config{
		MinVersion: tls.VersionTLS13,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{originDER},
			PrivateKey:  originKey,
		}},
	}
	origin.StartTLS()
	defer origin.Close()

	x25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	seed := [32]byte{4, 8, 15, 16, 23, 42}
	public, private := mldsa65.NewKeyFromSeed(&seed)
	originAddr := strings.TrimPrefix(origin.URL, "https://")

	for _, tc := range []struct {
		name       string
		verifyKey  []byte
		wantReject bool
	}{
		{name: "valid-transcript", verifyKey: public.Bytes()},
		{name: "wrong-MLDSA-verification-key", verifyKey: func() []byte {
			wrongSeed := [32]byte{42, 23, 16, 15, 8, 4}
			wrongPublic, _ := mldsa65.NewKeyFromSeed(&wrongSeed)
			return wrongPublic.Bytes()
		}(), wantReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			serverConfig := &utls.RealityConfig{
				// REALITY's internal logging may outlive the subtest. Never call
				// t.Logf from that detached goroutine; it races with test teardown.
				// Also avoid printing handshake secrets into CI logs.
				Log: func(string, ...any) {},
				Type:        "tcp",
				Dest:        originAddr,
				PrivateKey:  x25519.Bytes(),
				Mldsa65Key:  private.Bytes(),
				ServerNames: map[string]bool{"example.com": true},
				ShortIds:    map[[8]byte]bool{{}: true},
				DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			}
			client, err := NewRealityClient(ctx, nil, "example.com", option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: "example.com",
				UTLS:       &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
				Reality: &option.OutboundRealityOptions{
					Enabled:       true,
					PublicKey:     base64.RawURLEncoding.EncodeToString(x25519.PublicKey().Bytes()),
					Mldsa65Verify: base64.RawURLEncoding.EncodeToString(tc.verifyKey),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			clientConn, serverConn := net.Pipe()
			deadline := time.Now().Add(25 * time.Second)
			if err := clientConn.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			if err := serverConn.SetDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() {
				_, handshakeErr := utls.RealityServer(ctx, serverConn, serverConfig)
				serverDone <- handshakeErr
			}()
			verified, err := client.(*RealityClientConfig).ClientHandshake(ctx, clientConn)
			if tc.wantReject {
				if err == nil {
					_ = verified.Close()
					t.Error("REALITY accepted a server signed by the wrong ML-DSA key")
				}
			} else if err != nil {
				t.Errorf("REALITY rejected a valid ML-DSA server handshake: %v", err)
			}
			if verified != nil {
				_ = verified.Close()
			}
			_ = clientConn.Close()
			_ = serverConn.Close()
			origin.CloseClientConnections()
			select {
			case serverErr := <-serverDone:
				if serverErr != nil && !errors.Is(serverErr, io.EOF) {
					t.Logf("REALITY server result: %v", serverErr)
				}
			case <-time.After(5 * time.Second):
				t.Error("REALITY server goroutines did not exit after disconnect")
			}
		})
	}
}

func TestXUIPanelRealityMLDSASigningKeyDiffers(t *testing.T) {
	privateKey := bytes.Repeat([]byte{3}, 32)
	if _, err := parseRealityMLDSAServerKey(base64.RawURLEncoding.EncodeToString(privateKey), privateKey); err == nil {
		t.Fatal("accepting a reused REALITY X25519 private key weakens key separation")
	}
}
