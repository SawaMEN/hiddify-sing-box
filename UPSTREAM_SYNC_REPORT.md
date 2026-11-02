# Upstream v5 sync report

- Fork base: `0337a5e570823b826634111dba8b6d3dd6e804c6`
- Original upstream: `004371d6be2395ee4379054a8cf113f339dbe231`
- Merge base: `6fe457e69b87bca66f959f8982e1112bcae31625`
- Fork-exclusive paths: 314
- Deferred: upstream-only monitoring, sequential DNS and MASQUE registration helpers requiring a full state-machine port; fork MASQUE outbound preserved whole; existing fork monitor left unchanged.
- Paths requiring fork-preferred conflict handling: 0
- Validation: production-like build and short tests with all usual tags except pre-existing badlinkname, namespace unshare test skipped for runner privileges, libbox compiled, boxdd vetted, custom protocol tests passed.
- Known pre-existing gap: badlinkname runtime.allgs cannot link on fork Go 1.26.10; its implementation was not changed.
- Test environment: TestUnshareNamespace requires privileges not granted to GitHub Actions; the test is not run in this unprivileged CI job.
- Existing linker gap: libbox and boxdd test binaries and boxdd executable cannot link runtime/pprof.parseProcSelfMaps under Go 1.26.10; their original source is unchanged.

## Conflict paths (local fork version takes precedence)

No unresolved conflicts.
