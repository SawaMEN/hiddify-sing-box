//go:build with_utls

package tls

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
	"github.com/sagernet/sing-box/option"
)

func TestXUIPanelRealityMLDSAKeyValidation(t *testing.T) {
	if key, err := parseRealityMLDSAPublicKey(""); key != nil || err != nil {
		t.Fatalf("optional key: %v %v", key, err)
	}
	for _, value := range []string{"not/base64", base64.RawURLEncoding.EncodeToString([]byte("short"))} {
		if _, err := parseRealityMLDSAPublicKey(value); err == nil {
			t.Fatalf("accepted invalid mldsa65_verify: %q", value)
		}
	}
	seed := [32]byte{1, 2, 3}
	public, _ := mldsa65.NewKeyFromSeed(&seed)
	encoded := base64.RawURLEncoding.EncodeToString(public.Bytes())
	decoded, err := parseRealityMLDSAPublicKey(encoded)
	if err != nil || !bytes.Equal(public.Bytes(), decoded.Bytes()) {
		t.Fatalf("valid ML-DSA public key was rejected: %v", err)
	}
}

func TestXUIPanelRealityMLDSASignatureBinding(t *testing.T) {
	seed := [32]byte{6, 7, 8}
	public, private := mldsa65.NewKeyFromSeed(&seed)
	authKey := []byte("test-reality-shared-secret")
	pub := []byte("test-reality-ed25519-public-key")
	hello := []byte("client hello")
	serverHello := []byte("server hello")
	mac := hmac.New(sha512.New, authKey)
	mac.Write(pub)
	certSignature := mac.Sum(nil)
	mac.Write(hello)
	mac.Write(serverHello)
	signature := make([]byte, mldsa65.SignatureSize)
	if err := mldsa65.SignTo(private, mac.Sum(nil), nil, false, signature); err != nil {
		t.Fatal(err)
	}
	if !mldsa65.Verify(public, mac.Sum(nil), nil, signature) {
		t.Fatal("valid REALITY transcript signature was rejected")
	}
	macBad := hmac.New(sha512.New, authKey)
	macBad.Write(pub)
	macBad.Write([]byte("different client hello"))
	macBad.Write(serverHello)
	if mldsa65.Verify(public, macBad.Sum(nil), nil, signature) {
		t.Fatal("ML-DSA signature accepted a modified handshake")
	}
	if len(certSignature) != sha512.Size {
		t.Fatal("unexpected REALITY authentication MAC size")
	}
}

func TestXUIPanelRealityMLDSAServerFailsClosed(t *testing.T) {
	_, err := NewRealityServer(context.Background(), nil, option.InboundTLSOptions{
		Reality: &option.InboundRealityOptions{Mldsa65Seed: "configured-but-unsupported"},
	})
	if err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("unavailable ML-DSA signing was silently accepted: %v", err)
	}
}
