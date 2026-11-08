package xhttp

import (
	"strings"
	"testing"
)

func TestRandStringFromCharsetFullByteAlphabet(t *testing.T) {
	alphabet := make([]byte, 256)
	for i := range alphabet {
		alphabet[i] = byte(i)
	}
	got, ok := randStringFromCharset(128, string(alphabet))
	if !ok || len(got) != 128 {
		t.Fatalf("256-byte charset must be supported, got length=%d valid=%v", len(got), ok)
	}
	if _, ok := randStringFromCharset(1, string(alphabet)+"!"); ok {
		t.Fatal("charsets larger than 256 bytes must be rejected")
	}
	if got, ok := randStringFromCharset(0, "A"); ok || got != "" {
		t.Fatal("zero-length request must be rejected")
	}
	if got, ok := randStringFromCharset(16, strings.Repeat("X", 1)); !ok || got != strings.Repeat("X", 16) {
		t.Fatalf("single-character alphabet unexpected result: %q, %v", got, ok)
	}
}
