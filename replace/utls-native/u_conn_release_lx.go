// Copyright 2026 sing-box-lx authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tls

// ReleaseHandshakeState drops the client state a UConn keeps after a completed
// handshake that the caller cannot reach: the ClientHelloSpec the extensions
// were built from (it pins every extension object for the life of the
// connection) and the ECH context and extension shortcut. Nothing reads them
// once the handshake is complete: the spec is only used while the hello is not
// yet built, the ECH context only at the start of the handshake.
//
// It is a no-op before the handshake completes and on a TLS 1.2 connection
// that may still renegotiate: a renegotiation rebuilds the hello from this
// state. Browser presets enable renegotiation through their renegotiation_info
// extension, so this keeps the state on TLS 1.2 with them; TLS 1.3 has no
// renegotiation. The caller clears HandshakeState and Extensions itself.
func (uconn *UConn) ReleaseHandshakeState() {
	if !uconn.isHandshakeComplete.Load() {
		return
	}
	if uconn.vers != VersionTLS13 && uconn.config.Renegotiation != RenegotiateNever {
		return
	}
	uconn.clientHelloSpec = nil
	uconn.echCtx = nil
	uconn.ech = nil
}
