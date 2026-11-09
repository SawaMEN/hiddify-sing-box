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

	"github.com/sagernet/sing-box/option"

	"github.com/cloudflare/circl/sign/mldsa/mldsa65"
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

func TestXUIPanelRealityMLDSAServerRejectsInvalidSeed(t *testing.T) {
	_, err := NewRealityServer(context.Background(), nil, option.InboundTLSOptions{
		Reality: &option.InboundRealityOptions{
			PrivateKey: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
			Mldsa65Seed: "configured-but-unsupported",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid REALITY mldsa65_seed") {
		t.Fatalf("malformed ML-DSA seed was silently accepted: %v", err)
	}
}

func TestXUIPanelRealityMLDSAServerKeyDerivation(t *testing.T) {
	privateKey := bytes.Repeat([]byte{1}, mldsa65.SeedSize)
	if key, err := parseRealityMLDSAServerKey("", privateKey); err != nil || key != nil {
		t.Fatalf("empty ML-DSA seed should keep classical REALITY: %v, %v", key, err)
	}
	for _, invalid := range []string{"invalid/base64", base64.RawURLEncoding.EncodeToString([]byte("too short"))} {
		if _, err := parseRealityMLDSAServerKey(invalid, privateKey); err == nil {
			t.Fatalf("invalid ML-DSA seed accepted: %q", invalid)
		}
	}
	if _, err := parseRealityMLDSAServerKey(base64.RawURLEncoding.EncodeToString(privateKey), privateKey); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("reused REALITY private key accepted as ML-DSA seed: %v", err)
	}
	seed := [32]byte{4, 5, 6}
	_, expectedPrivate := mldsa65.NewKeyFromSeed(&seed)
	key, err := parseRealityMLDSAServerKey(base64.RawURLEncoding.EncodeToString(seed[:]), privateKey)
	if err != nil || !bytes.Equal(key, expectedPrivate.Bytes()) {
		t.Fatalf("valid ML-DSA seed derived incorrect private key: %v", err)
	}
	if _, err := mldsa65.Scheme().UnmarshalBinaryPrivateKey(key); err != nil {
		t.Fatalf("derived REALITY ML-DSA key rejected by signer: %v", err)
	}
}
