# Hiddify v5 synchronization — 2026-11-04

## Source

- Upstream: `hiddify/hiddify-sing-box:v5`, `2f43880d30e1f4d71154b0c9d3fa309c4ece4b26`.
- Previous synchronized upstream: `004371d6be2395ee4379054a8cf113f339dbe231`.
- Fork baseline: `9df34013f49cf301a42e0254c2df8fc6cbe746e8` (`main`).
- All 16 new upstream commits are included. The fork delta was rebased locally onto the new upstream tip. The integrated tree is published as a compatibility commit on the existing fork history; this source SHA is the baseline for the next synchronization.

## Changes

- Shared resolver handling and adaptive pools for DNSTT and the new MasterDNS outbound, including duplication, rate-limit recovery, benchmarks and regression tests.
- Verified and learned IPv6 capability per outbound, declared capability, cached state and route fallback instead of assuming that opening a proxy connection proves remote IPv6 works.
- Hot-reload monitoring retests, staged results and idle recovery; retain the fork's atomic lifecycle state and rollback when a bounded task queue is full.
- Xray 26.10.31 finalmask, merge/OOB, legacy XDNS configuration support and Xray fingerprint aliases.
- Canonical Xray XDrive storage transport replaces the duplicated storage implementation. Google Drive, S3, templates and local storage remain available; retain the old `front_address`/`front_port`, JSON-encoded template strings and inbound `storage_dialer` options.
- Go 1.27.3 and updated upstream dependencies, including the upstream Psiphon TLS pin `30b2320c` to prevent its Go 1.27 initialization panic. Restore `godebug multipathtcp=0` as required by the network dialer defaults.

## Fork compatibility

VetrOFFProxy, OpenFlux, OLCRTC, FPTN, PingTunnel, custom Sudoku, Snell and the existing WARP scanner remain in the fork. Existing `smart_dns_pool` service configurations remain accepted while DNS tunnels can use the new per-outbound pool. XDrive is included with `with_xdrive`; the Makefile default includes it. Consumers using their own tags must add that tag when using XDrive.

The earlier fork adaptations to the WARP/MASQUE device lifecycle remain; this delta does not replace that subsystem. Live WARP/Psiphon checks require network access and their documented opt-in environment variables.

## Validation

The pull request runs the standard platform Test and Lint workflows plus production-tag vet, race regressions and the full production-tag suite in `Upstream compatibility`. Final validation results are recorded after those checks complete. Schema and module metadata are generated from the integrated source, rather than copied over the fork's options.
