> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Follow-up ten — 2026-10-23

Implementation checkout: `/root/work/astronomer-fixes`, branch
`fix/2.1-runtime-verification`. This report extends Plan 032 §20.8 and preserves
all prior R01–R16 work. No commit, push, release, deployment or cluster deletion.

| Follow-up | Result and retained acceptance work |
|---|---|
| 1. Monaco source rebuild | Implemented. Monaco 0.57.0 ESM uses locked DOMPurify 3.4.17; shared resolver rejects old vendored sanitizer in the module graph. AMD copying removed. |
| 2. Shipped assets | Implemented. Hash inventory covers editor, workers, chunks, CSS/font; dist gate binds versions to lock. Tampered bytes, missing worker and stale sanitizer manifest reject. Sanitizer negative/positive tests execute the same build resolver. Three Node tests pass. |
| 3. Editor browsers | Chromium, Firefox and WebKit pass real rapid typing/save, external value reload, JSON worker diagnostics, no console errors and same-origin requests under the exact nginx CSP. Regression checks added to CI. Found and fixed nested lazy export and stale controlled YAML value overwrites using synchronous edit commits. |
| 4. Pagination timing | Targeted 4 tests and full 351-file/2,435-test suite pass. Original timing failure not reproduced; no deadline increase or unproven production fix. Causal closure remains open. |
| 5. Retired Dex | Focused handler/worker and real PostgreSQL advisory locks pass. All eight old contracts mapped in dex-coverage-map.md. Several operation-to-rollout/provider scenarios remain uncovered; skips retained. |
| 6. Private Helm | Owned private interface 172.17.0.1 exercises previously skipped fixtures; package passes. No network guard changes. |
| 7. Canonical proxy | Package passes with actual cryptographic fixture verification and foreign signer rejection. Fixed stale seven-image assertions to current six-role manifest (server/worker/agent/frontend/shell/dr). Synthetic children remain bounded test fixtures, not official publisher/live upgrade proof. |
| 8. OCI ingestion | New integration test passes with a real TLS distribution registry, real uploaded Helm archives and migrated PostgreSQL 17.11. Both versions persist; repeat ingestion creates zero duplicates. Empty unconditional live-test placeholder removed. Public-host/authenticated external-registry qualification is separate. |
| 9. Charlie suspension | Pending-ceiling regression passes; no premature runtime shutdown. Corrected operations guide's unsupported workload-removal claims. Live feature suspension remains blocked on supported agent readiness/bridge confirmation; no direct DB or workload mutation. |
| 10. Qualification tooling | Historical runner reviewed; version and independent-host acceptance gaps identified. Exact port sequence and negative tests documented in qualification-port-review.md. Source port, immutable candidate, dedicated targets and all 174 lifecycle cases remain open. |

## Evidence and limits

- `frontend-build-verified.log`: TypeScript, production build, emitted CSP and
  sanitizer inventory checks pass. `frontend-lint-final.log`: ESLint passes.
- `sanitizer-final.log`: real rebuilt sanitizer and emitted-provenance negatives,
  3/3 tests pass; `npm-audit.json`: locked npm audit.
- `editor-browser-verified.log`: Chromium/Firefox/WebKit, 3/3 pass. Browser
  fixture is a separate build using production components and exact public
  Monaco assets, not a deployed candidate acceptance test.
- `frontend-tests.log`: 2,435 tests pass; this preceded the YAML synchronous
  callback change, whose behavior is covered by the final three-browser test.
- `bundle-final.log`: bootstrap 321,648; login 350,072; app 453,815 gzip bytes.
  All existing limits unchanged and satisfied; 455 application chunks. Monaco
  remains separately lazy-loaded public assets, outside these eager closures.
- `private-fixtures-final.log`: helmruntime and upgradecli pass with owned private
  interface. Initial failures retained: stale image fixture, then `/tmp` capacity
  reserve. Final run uses `/root/work/astronomer-test-tmp` on the workspace disk;
  production reserve enforcement unchanged.
- `oci-integration.log`: real registry/database ingestion pass. Loopback TLS
  client injected through existing operation-scoped composition; this is not a
  claim that a private destination passes public-only SSRF policy.
- `dex-charlie.log`, `dex-postgres.log`, `charlie-pending.log`: scoped passes.
- Initial browser failures retained to show diagnosis. The abandoned filename
  `editor-browser-paste.log` still contains keyboard-typing failures: the attempted
  test edit did not apply. Final verification retains rapid keyboard typing.
- T3 preview unavailable: Chrome cannot start its sandbox as root. Repository
  Playwright workflow used after explicit preview-open error.

Source/release boundary: original ui-demo remains preserved; replacement and
site-demo were not modified this turn. No existing workloads were repurposed as
qualification fixtures. R13 source remediation does not itself deploy a fix.

`browser-components-final.log`: all 12 desktop component checks pass under the
production CSP, including prior modal/table/accessibility/reliability coverage.

## Final gate

`make-check.log` exits 0: Go lint, vet, formatting, four production builds and
race suite pass. JUnit: **15,692 total; 15,661 passed; 31 skipped; zero failures
or errors**. The 31 ordinary-run skips comprise the previously separately
qualified seven tool/environment cases, 16 now-executed private Helm/proxy cases,
and eight mapped retired Dex contracts. The empty OCI skip is gone; real OCI
coverage runs under the integration tag. `oci-lint.log` reports zero issues with
that tag. All **74 source hashes match** `source-manifest.json` after verification.
`verification-summary.json` records exact counts and the remaining open tasks.
