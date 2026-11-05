# Hiddify v5 synchronization — 2026-11-05

## Source

- Upstream: `hiddify/hiddify-sing-box:v5`, `6a545ead13f09130c7c802fa948b9d22f33c5000`.
- Previous synchronized upstream: `1614c5edb52dd44a4cc079201ba1e790fdfad8a0`.
- Fork baseline: `3d4545afd0b6b27048b88a697a1dfae9a4bedae7` (`main`).
- All 3 new upstream commits since the previous synchronization are included without upstream merge commits. The fork's stack ownership callbacks and previous Bond/XHTTP compatibility repairs are retained.

## Changes

- Latest lifecycle fixes: close UDP sockets when their local address family changes; update AWG netstack close safety; pin the hiddify/sing cached-buffer double-release fix in both Go modules.

- Mirage TLS record splitting from the latest upstream commit, using the pinned MIT-licensed library, is included.

- Soar DNS tunnel and shared discovery/refilling for DNS pools.
- Monitoring joins simultaneous tests, records failure reasons and stabilizes lowest-delay selection; balancer selection rules are exported.
- WARP restart/failover safety, refused MASQUE-device recovery and detailed status errors; status marks are rendered by the app.
- Shared resolver handling and adaptive pools for DNSTT and the new MasterDNS outbound, including duplication, rate-limit recovery, benchmarks and regression tests.
- Verified and learned IPv6 capability per outbound, declared capability, cached state and route fallback instead of assuming that opening a proxy connection proves remote IPv6 works.
- Hot-reload monitoring retests, staged results and idle recovery; retain the fork's atomic lifecycle state and rollback when a bounded task queue is full.
- Xray 26.10.31 finalmask, merge/OOB, legacy XDNS configuration support and Xray fingerprint aliases.
- Canonical Xray XDrive storage transport replaces the duplicated storage implementation. Google Drive, S3, templates and local storage remain available; retain the old `front_address`/`front_port`, JSON-encoded template strings and inbound `storage_dialer` options.
- TCP Fast Open dependency `tfo-go v2.4.2` matches the Go 1.27 internal socket API; the Windows hot-reload traffic test retains TCP Fast Open.
- Go 1.27.3 and updated upstream dependencies, including the upstream Psiphon TLS pin `30b2320c` to prevent its Go 1.27 initialization panic. Restore `godebug multipathtcp=0` as required by the network dialer defaults.

## Fork compatibility

VetrOFFProxy, OpenFlux, OLCRTC, FPTN, PingTunnel, custom Sudoku, Snell and the existing WARP scanner remain in the fork. Existing `smart_dns_pool` service configurations remain accepted while DNS tunnels can use the new per-outbound pool. XDrive remains registered in ordinary fork builds; `with_xdrive` also enables its upstream integration tests. Both `final_mask` and upstream `finalmask` configuration spellings are supported, with the legacy key taking precedence.

WARP/MASQUE now uses the latest upstream device-pool, refusal recovery and scope-owned lifecycle. The fork retains explicit registration-detour policy, IPv4/IPv6 profile validation and its adapted gVisor stack. Standalone `masque` requires an enrolled `device`; automatic registration uses a `warp` endpoint with `mode: masque`. The fork's `IsReady` adapter contract and legacy `pool_tolerance` configuration are retained alongside optional detailed status. Live WARP/Psiphon checks require network access and their documented opt-in environment variables.

## Integration regressions

- Address-change, cached-buffer and AWG close regressions reproduce the old failures before the update. After the update, cached-connection and device regressions pass five race runs, and AWG close passes three race runs. These packages are included in continuous race coverage.

- Successful sub-millisecond URL tests are recorded as 1 ms; zero remains reserved for an unmeasured result, preventing unnecessary staged retries.

- Monitoring publishes completion before its result, so a subsequent URL stage starts a fresh test instead of joining a settled test; completion signals belong to their execution.
- MASQUE unwraps mapped IPv4 destinations and uses its IPv6 local address for IPv6 UDP. The fork's gVisor forwarder Start/Close ownership is retained.
- DNS auto-resolver tests cover both bundled and dynamically discovered candidates.

## Validation

This follow-up synchronizes upstream `6a545ead` after PR #19. Local before/after evidence covers all three upstream lifecycle fixes. Full platform Test/Lint, Snell interoperability, production vet/race/full tests and Xray interoperability run on the follow-up PR. The earlier main-branch Snell failure is retried separately to distinguish a transient official-server timeout from a code regression.

The pull request runs the standard platform Test and Lint workflows plus production-tag vet, race regressions and the full production-tag suite in `Upstream compatibility`. The original synchronization was superseded by PR #18 and retained in main. Finishing changes update the source baseline and repair the remaining CI regressions; exact-head results are in GitHub Actions. Local regression checks cover monitoring, balancer, WARP/MASQUE, DNSTT/MasterDNS, Soar, IP lookup, schema and options. Schema and module metadata are generated from the integrated source, rather than copied over the fork's options.
