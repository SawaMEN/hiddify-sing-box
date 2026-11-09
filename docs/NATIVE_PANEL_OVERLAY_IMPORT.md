# Native panel overlay import inventory

Status: **source import completed for the 15 Sing-box-targeted groups; no CI/E2E or wire-parity certification**. This is a source integration snapshot, **not a stable release**.

This inventory maps the sixteen legacy overlay groups from `SawaMEN/3x-ui/deploy/hiddify` to their candidate native sources. Old patches **must not** be blindly re-applied over native source. The remaining task is to fix build/test failures and check behavioral parity, especially for REALITY and VLESS Reverse.

## Imported Sing-box source groups (15/15: source present, tests outstanding)

| Legacy overlay | Native source or checked-in dependency | Source integration |
|---|---|---|
| `clash-user.patch` | `experimental/clashapi/trafficontrol/tracker.go` | baseline, PR #23 |
| `sniff-override.patch` | `option/rule_action.go`, `route/rule/rule_action.go` | baseline, PR #23 |
| `http1-options.patch` | `option/http.go` | baseline, PR #23 |
| `hysteria-safety.patch` | `protocol/hysteria/inbound.go`, `protocol/hysteria2/inbound.go` | baseline, PR #23 |
| `inbound-finalmask.patch` | `adapter/inbound/`, `common/listener/` and `option/inbound.go` | baseline, PR #23 |
| `stream-handshake.patch` | `transport/trojan/`, `transport/v2rayhttpupgrade/` | baseline, PR #23 |
| `explicit-cipher-suites.patch` | `common/tls/` | baseline, PR #23 |
| `vless-handshake.patch` | `protocol/vless/` | baseline, PR #23 with PR #22 regressions |
| `transport-balancer.patch` | `protocol/group/selector.go`, `protocol/group/xui_balancer.go`, `option/`, `common/urltest/`, `transport/v2raykcp/`, `transport/v2rayxhttp/` | baseline, PR #27, PR #33 |
| `vless-reverse.patch` | `option/vless.go`, `protocol/vless/xui_reverse.go`, VLESS inbound/outbound and v2rayapi stats | PR #32 |
| `protection-options.patch` | `option/tls.go`, `common/tls/reality_client.go`, `common/tls/reality_server.go`, VLESS preconnect and handler | baseline, PR #28, PR #29 |
| `sing-vmess-protection.patch` | `third_party/sing-vmess/vless/` and VLESS handler | PR #28 |
| `sing-vmess-reverse.patch` | `third_party/sing-vmess/vless/` reverse wire and Vision framing | PR #32 |
| `utls-protection.patch` | `replace/utls-native/reality.go` and related native module; root go.mod replace | PR #29 |
| `xray-runtime-compat.patch` | `third_party/xray-core/` plus root go.mod replace | PR #30 |

The native implementations do **not** necessarily reproduce the exact lines of the legacy patches. Do not infer full feature parity merely from the existence of a source file.

## Core-only group (1/16)

`runtime-dependencies.patch` targets **the Hiddify-core module's `go.mod`**. Applying it to Sing-box's `go.mod` would modify the wrong module. This group is reserved for the **later** Hiddify-core stage after Sing-box integration is verified. The modules required by native Sing-box dependencies are separately pinned through the Sing-box `go.mod` and vendored replacements.

## Immutable reference and import commits

- Pre-import backup: `backup/pre-all-overlays-20261109` at `abe6d7fd1f395aacc9c15030bcfe3011b6a56e50`.
- mKCP config guards: PR #33, merged `ca6f3fda52a1048b10f7a4be0f245b3e1e7f212e`.
- VLESS Reverse portal, bridge and vendored reverse wire: PR #32, merged `a01549a07a1c5601e8c2f183b9a7ef7c10045819`.
- REALITY ML-DSA-65 signing via uTLS source: PR #29, merged `d1afca750b6db51c5d44bbdcbb3029e641f70748`.
- Xray runtime source: PR #30, merged `b04890e3c00717b65f6ae203694b26e4a510dba7`.

## Repair and release gates (not yet done)

1. Repair compile and type errors introduced by the combined native source trees, ensure correct module replacements and bundled license/provenance.
2. Run all CI matrices and `-race` tests; prevent leaks, deadlocks and connection double-close; keep security checks fail-closed.
3. Validate VLESS Reverse portal/bridge TCP/UDP wire compatibility against an actual Xray peer.
4. Validate REALITY ML-DSA-65 positive handshake plus tampered signature/transcript rejection.
5. Test Vision padding/testseed and XHTTP/mKCP, balancer/observer behavior, and cross-platform builds.
6. Only after these checks pass should `hiddify-core` adopt the verified immutable Sing-box SHA and later remove superseded panel patch application.

**Do not use this import snapshot as a production kernel.** It deliberately merges candidate source before green CI at the project owner's request; the next phase is to fix and verify the integrated code.
