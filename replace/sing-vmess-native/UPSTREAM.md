# Native sing-vmess extension for panel protocol parity

Source: [starifly/sing-vmess](https://github.com/starifly/sing-vmess), pinned release tag `v0.2.7-mod.9`. Upstream license is retained in `LICENSE`.

The exact 41 hunks from `sing-vmess-protection.patch` and `sing-vmess-reverse.patch` have been integrated into these source files. The old runtime patching is not used for this module when imported by sing-box. This adds Vision testseed validation, safe TLS field extraction, serialized Vision reader/filter access, reverse command wire framing, and authenticated reverse handler API.

Full production parity still depends on top-level VLESS reverse implementation, REALITY signing, Xray interoperability and integration tests; do not remove remaining build overlays or publish a production kernel solely because this module compiles.
