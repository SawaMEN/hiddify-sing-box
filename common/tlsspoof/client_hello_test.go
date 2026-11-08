package tlsspoof

import (
	"encoding/binary"
	"strings"
	"testing"

	tf "github.com/sagernet/sing-box/common/tlsfragment"

	"github.com/stretchr/testify/require"
)

// x25519MLKEM768 is the IANA code point for the post-quantum hybrid named
// group (0x11EC). The fake ClientHello must never carry it — its 1184-byte
// key share is the reason kernel-generated ClientHellos exceed one MSS, and
// the reason this builder has to force CurvePreferences.
const x25519MLKEM768 uint16 = 0x11EC

func TestBuildFakeClientHello_ParsesWithSNI(t *testing.T) {
	t.Parallel()
	record, err := buildFakeClientHello("example.com")
	require.NoError(t, err)

	serverName := tf.IndexTLSServerName(record)
	require.NotNil(t, serverName, "output must parse as a ClientHello")
	require.Equal(t, "example.com", serverName.ServerName)

	recordLen := binary.BigEndian.Uint16(record[3:5])
	require.Equal(t, len(record)-5, int(recordLen),
		"record length header must match on-wire record size")
	handshakeLen := int(record[6])<<16 | int(record[7])<<8 | int(record[8])
	require.Equal(t, len(record)-5-4, handshakeLen,
		"handshake length header must match handshake body size")
}

// TestBuildFakeClientHello_FitsOneSegment is the regression guard for the
// whole point of the rewrite: the fake must never need fragmenting on a
// standard 1500-byte path MTU. 1200 leaves ~260 bytes for IP+TCP headers and
// a generous safety margin — the X25519MLKEM768 ClientHello this replaces
// hit ~1400+.
func TestBuildFakeClientHello_FitsOneSegment(t *testing.T) {
	t.Parallel()
	for _, sni := range []string{"a.io", "example.com", strings.Repeat("a", 253)} {
		record, err := buildFakeClientHello(sni)
		require.NoError(t, err, "sni=%q", sni)
		require.Less(t, len(record), 1200, "sni=%q built %d bytes", sni, len(record))
	}
}

// TestBuildFakeClientHello_NoPostQuantumKeyShare catches regressions that
// would accidentally pull an X25519MLKEM768 key share (the reason the prior
// implementation had to fragment) back into the fake — e.g. if CurvePreferences
// stopped being respected by a future Go version.
func TestBuildFakeClientHello_NoPostQuantumKeyShare(t *testing.T) {
	t.Parallel()
	record, err := buildFakeClientHello("example.com")
	require.NoError(t, err)

	// Examine the TLS extension structures, not arbitrary random payload
	// bytes: random and key material can contain 0x11ec by coincidence.
	require.GreaterOrEqual(t, len(record), 9)
	require.Equal(t, byte(1), record[5], "handshake must be ClientHello")
	pos := 9 // TLS record (5) + handshake header (4)
	take := func(n int) []byte {
		t.Helper()
		require.GreaterOrEqual(t, n, 0)
		require.GreaterOrEqual(t, len(record)-pos, n, "truncated ClientHello")
		value := record[pos : pos+n]
		pos += n
		return value
	}
	take(2 + 32) // client_version + random
	take(int(take(1)[0])) // legacy_session_id
	take(int(binary.BigEndian.Uint16(take(2)))) // cipher_suites
	take(int(take(1)[0])) // compression_methods
	extensions := take(int(binary.BigEndian.Uint16(take(2))))
	require.Equal(t, len(record), pos, "trailing handshake bytes")
	foundGroups, foundShare := false, false
	for len(extensions) > 0 {
		require.GreaterOrEqual(t, len(extensions), 4, "truncated extension header")
		kind := binary.BigEndian.Uint16(extensions[:2])
		length := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		require.GreaterOrEqual(t, len(extensions), length, "truncated extension body")
		body := extensions[:length]
		extensions = extensions[length:]
		switch kind {
		case 10: // supported_groups: uint16 length + repeated uint16 group
			foundGroups = true
			require.GreaterOrEqual(t, len(body), 2)
			require.Equal(t, len(body)-2, int(binary.BigEndian.Uint16(body[:2])))
			require.Zero(t, (len(body)-2)%2)
			for groups := body[2:]; len(groups) > 0; groups = groups[2:] {
				require.NotEqual(t, x25519MLKEM768, binary.BigEndian.Uint16(groups[:2]),
					"hybrid group advertised in supported_groups")
			}
		case 51: // key_share: uint16 vector length, then (group, len, key)
			foundShare = true
			require.GreaterOrEqual(t, len(body), 2)
			require.Equal(t, len(body)-2, int(binary.BigEndian.Uint16(body[:2])))
			for shares := body[2:]; len(shares) > 0; {
				require.GreaterOrEqual(t, len(shares), 4)
				require.NotEqual(t, x25519MLKEM768, binary.BigEndian.Uint16(shares[:2]),
					"hybrid group used in key_share")
				keyLength := int(binary.BigEndian.Uint16(shares[2:4]))
				shares = shares[4:]
				require.GreaterOrEqual(t, len(shares), keyLength)
				shares = shares[keyLength:]
			}
		}
	}
	require.True(t, foundGroups, "missing supported_groups extension")
	require.True(t, foundShare, "missing key_share extension")
}

// TestBuildFakeClientHello_RandomizesPerCall ensures crypto/tls generates a
// fresh random + session_id + key_share on every call, as required to avoid
// trivial fingerprinting of the spoof.
func TestBuildFakeClientHello_RandomizesPerCall(t *testing.T) {
	t.Parallel()
	first, err := buildFakeClientHello("example.com")
	require.NoError(t, err)
	second, err := buildFakeClientHello("example.com")
	require.NoError(t, err)
	require.NotEqual(t, first, second,
		"repeated calls must produce distinct bytes (random/session_id/key_share must vary)")
}

func TestBuildFakeClientHello_RejectsEmpty(t *testing.T) {
	t.Parallel()
	_, err := buildFakeClientHello("")
	require.Error(t, err)
}
