> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Plan 032 R04–R06 implementation evidence

Source is `/root/work/astronomer-fixes`, branch `fix/2.1-runtime-verification`, baseline recorded in `source-manifest.json`. All changes are local and undeployed. The existing [Plan 032 continuation](../../../../032-technology-guide-alignment-and-latest-stable-master-plan.md:1426) owns tasks and remaining qualification. The patch includes the earlier R01–R03 auth slice; its original evidence is preserved separately.

R04 binds idempotency to actor, method, concrete escaped path, resolved project and client key, using unambiguous length framing. Existing scope resolution and authorization precede replay. Tests reproduce the baseline target/project and delimiter collisions, then verify exact retries, distinct targets/projects, body/query conflict, concurrency, transient failure, expiration and invalid/unauthorized requests. The cache remains process-local; there is no persisted-key migration.

R05 adds a closed archive representation for the two documented optional trust directories. Legacy flat bundles remain readable and retain their format; long Secret filenames use deterministic PAX when USTAR cannot encode them. The reader validates every member before creating a private destination. Limits are 1 MiB per file, 16 MiB payload and 1,024 files. Traversal, absolute paths, links, unexpected nesting, duplicates and parent/file collisions fail closed. Source reads are bounded and confined with `os.OpenRoot`; extraction refuses an existing destination and creates 0700 directories/0600 files. Older extractors reject new nested bundles, so the runbook requires the producer's extractor or a newer compatible one.

The chart now copies visible projected Secret keys and ordinary dot-prefixed keys instead of copying Kubernetes `..data`/timestamp directories. The test renders the actual chart, stages synthetic projected volumes, builds the real `dr-crypto` CLI, runs the chart's staging/encryption script, decrypts and extracts, then checks recovered files. It covers no trust, either trust type, both, and empty configured trust failing before any encrypted envelope. Database dump, S3 publication and isolated database restore are not exercised by this test; the existing manifest-last ordering is retained.

R06 aligns probe, inventory and system Kustomization health. The source-controller exception requires the observed generation, all desired replicas updated with no old extras, and at least one ready/available artifact-serving leader. It rejects replica failures, deletion, lost leadership and incomplete upgrades. Inventory retains the actual replica counts. System `wait:true` remains enabled and other Deployments require every desired replica ready/available. No signed Flux distribution or running resource was patched.

The pinned [kustomize-controller v1.9.6](https://github.com/fluxcd/kustomize-controller/blob/v1.9.6/internal/controller/kustomization_controller.go) installs custom health readers for its status poller, including the service-account impersonation path used here. [cli-utils v1.2.3](https://github.com/fluxcd/cli-utils/blob/v1.2.3/pkg/kstatus/polling/polling.go) orders custom readers ahead of defaults. The exact [Flux status evaluator](https://github.com/fluxcd/pkg/blob/runtime/v0.110.3/runtime/cel/status_evaluator.go) checks observed generation and evaluates in-progress, failed, then current. Fifteen emitted-policy fixtures pass that pinned implementation. Read-only snapshots of all three replacement controllers also evaluate Current; this is evidence about the policy against observed status, not a live rollout/failover qualification.

| Evidence | Command/result |
| --- | --- |
| `idempotency-before.log`, `idempotency-after.log` | Focused middleware/server regressions: baseline fails, fixed source passes. |
| `backup-before.log`, `backup-unit.log`, `backup-bounds.log`, `backup-final.log` | Baseline optional trust fails; archive roundtrip, deterministic long filenames, limits and rejection tests pass after implementation. |
| `backup-cli.log`, `backup-focused.log` | Real CLI and rendered chart script matrix passes. |
| `standby-before.log` | Overlay restores baseline probe/inventory/system code: expected failures include healthy standby degradation, false-ready incomplete upgrades and missing Kustomization health expressions. |
| `standby-focused.log` | First package run caught an older synthetic standby fixture that declared two desired but only one updated/total replica. Fixture corrected to represent the intended two-pod deployment; no production check weakened. |
| `standby-cel.log`, `flux-health-fixtures.json` | Source tests and 15 sanitized expected health states pass. |
| `pinned-flux-evaluator.log`, `pinned-flux-versions.txt`, `pinned-flux-evaluator.go.txt` | Exact evaluator versions, harness and all 15 passing cases. Harness runs in a separate module, preserving repository dependency versions. |
| `flux-health-live-fixtures.json`, `pinned-flux-live-status.log` | Timestamped, sanitized controller status snapshots; all three pass the new evaluator. No live mutation. |
| `make-check-initial.log` | Initial full gate caught archive implementation/test complexity. Refactored helpers; focused lint passes. |
| `make-check-informer-fixture.log`, `informer-fixture-after.log` | Full run found one older agent informer fixture missing total/updated replica counts (15,689 tests, 32 skipped, one failure). Corrected its completed-rollout status; focused race regression passes. |
| `flux-health-server-dry-run.json` | Replacement API accepted the exact generated policy with server dry-run; subsequent read confirms it was not persisted. |
| `make-check.log` | Final lint/vet/format/four-production-binary/race gate: **exit 0; 15,657 passed / 32 skipped / 0 failed**. Private root-filesystem TMPDIR and `GOFLAGS='-p=4'`; no capacity guard disabled. |
| `make-security-final.log` | Final `GOFLAGS='-p=4' make security`: exit 0, configured gosec clean, zero reachable vulnerabilities (one module-only advisory), 2,304 commits scanned/no leaks. The later change only corrects informer test fixture status, outside the production security scan. |
| `helm-lint.log` | `make helm-lint`: exit 0, chart lint/template passed. |
| `source-manifest.json`, `source.patch` | Baseline, SHA-256 of 25 changed source/test/dependency/doc files, and cumulative reviewable patch. |

Repository tests use the CEL version already selected by the dependency graph (v0.29.2); it is now an explicit test dependency. No existing dependency version was downgraded. The separate harness verifies compatibility with Flux's v0.26.1 CEL runtime.

Remaining: new immutable candidate, live Kustomization readiness beyond the progress deadline, actual leader loss/recovery, application lifecycle/durable status, and isolated backup→database restore. Original ui-demo remains preserved. R07–R16 and previously skipped external fixtures are not closed by these source fixes. Security/release sign-off remains blocked until candidate qualification.

`verification-summary.json` records exact counts and skip identities/reasons. All 25 source hashes match after the final gate. The 32 skips are unchanged from the auth slice; none of the new R04–R06 regressions was skipped. Original failures and intermediate fixture/lint corrections remain retained.
