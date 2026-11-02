# Upstream Hiddify v5 synchronization

## Source and preservation

- Upstream: `hiddify/hiddify-sing-box`, branch `v5`, commit `004371d6be2395ee4379054a8cf113f339dbe231` (latest verified on 2026-11-02).
- Initial fork baseline: `0337a5e570823b826634111dba8b6d3dd6e804c6`, original `main` `1b8cf92b812e925851afeadfb11329fe0bce25b0`.
- Git merge commit: `d74e42fbb4e3dc7f39e967bcde348c870cbaf5cb` with both fork and upstream parents.
- The guarded Git merge verified that 314 paths exclusive to the fork were not lost. No unresolved content conflicts.
- Main protocol implementations `vetroff`, `openflux`, `olcrtc`, `fptn`, `pingtunnel`, and the fork's `option/vetroff.go`, `option/xdrive.go`, and WARP scanner implementation were verified identical by Git blob SHA on the review branch and original `main`.

## Upstream adaptations integrated after the merge

- IPv6 probe and `IPv6Capability` service to support `ipv6_by_outbound` and `ipv6_unavailable_via` rules, plus upstream regression tests.
- Sequential DNS failover with per-member time budgets, error TTL, recovery tracking and regression tests. Existing `multi`, parallel, stable and fastest modes retained.
- Monitor selected-outbound priority on startup and selector changes, without replacing the fork's existing monitoring registry, worker queues or IP-info behavior.
- MASQUE/WARP outbound lifecycle, endpoint scanner, detour following, and safe device registration/re-registration with backoff, retaining public WARP outbound API and existing fork protocol integrations. Upstream registration regression tests restored.
- Cloudflare Trace body fetch integrated into the existing IP provider, without overwriting custom IP information providers.

## Deliberate compatibility limits

- The fork's custom monitoring worker and event state machine remains authoritative; the upstream-only provisional *staged* test and hard-timeout *settle* helpers are not copied wholesale because they depend on a different scheduler and would overwrite the fork's monitoring logic. The documented `on_demand_timeouts` configuration exists, but this upstream scheduling behavior requires a separate reviewed integration.
- Go 1.26.10's `badlinkname` support and `runtime/pprof.parseProcSelfMaps` linker issue are pre-existing in the fork. The guarded merge validation compiled and tested all normal runtime packages under production-like tags *except* those existing incompatible linker paths; it vetted `boxdd`, compiled `libbox`, and skipped the privileged Linux namespace test on an unprivileged runner.

## Validation

- Initial guarded upstream Git merge: [successful GitHub Actions run](https://github.com/SawaMEN/hiddify-sing-box/actions/runs/41416486829); compilation, custom protocol tests and short regressions passed.
- All post-merge adaptation changes are subject to the repository's [standard Test](https://github.com/SawaMEN/hiddify-sing-box/actions/workflows/test.yml) and [Lint](https://github.com/SawaMEN/hiddify-sing-box/actions/workflows/lint.yml) checks on pull request #15.
- Do not merge the review branch unless the final CI checks pass and functional regression risks are reviewed.
