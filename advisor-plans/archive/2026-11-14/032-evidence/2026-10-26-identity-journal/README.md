> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Durable Identity provisioning journal — 2026-10-26

Plan 032 §20.13 task 6.3 source implementation. Migration 88 adds credential-free operation records. Preparation and revision-checked state changes share transactions with the audit outbox. Interrupted work cannot return to pending or be claimed again. Recovery advances the revision; completion requires authenticated durable credential material. Catalog/worker/API composition and upstream lifecycle operations remain open.

## Verification

- Five provisioning tests pass with `go test -race -tags integration ./internal/identity/provisioning -identity-test-kubeconfig=/root/.kube/astronomer-ui-demo-v211.yaml -count=1 -v`; zero skips. Real PostgreSQL 17.11 and Kubernetes, temporary namespaces only.
- Concurrency, restart ambiguity, immutable intent, cross-project denial, audit rollback, uninstall rejection, missing credentials, late completion fencing, credential recovery and credential-free journal/audit verified.
- Real 86→87→88 migration preserves local account, role, MFA and recovery snapshots. Fresh/idempotent/concurrent schema boot and shared Identity trust/binding tests pass under the race detector.
- Targeted golangci-lint over provisioning and database packages: zero issues.
- Targeted gosec: success, no findings.
- Entire deploy and releasecontract package suites pass. Candidate defaults, both chart schema constants and manifest target schema 88.
- All four command composition roots build successfully.
- `make sqlc` regenerated SQLC output; `git diff --check` passes.
- Final labeled Kubernetes namespace inventory empty; no application release or live application database was changed.

Earlier lint failure (test complexity) was fixed by extracting the concurrent-claim assertion helper. Earlier chart failures exposed the remaining schema-87 constants, which were updated consistently. Original logs are retained. This is targeted verification for the recorded source hashes, not a new full-repository acceptance claim. Test logs contain disposable PostgreSQL fixture credentials only.

## Boundaries

The journal is an internal service, not a caller authorization boundary. Worker/API wiring must enforce RBAC and verify the desired upstream specification. It does not recover a secret lost before persistence or revoke an upstream client. The schema represents rotation and retirement states, but their orchestration remains unimplemented. An advisory lock is not an atomic transaction across PostgreSQL, Kubernetes and an external issuer.

No upstream application was created. No live Dex workloads were deleted. No second-product SSO or issuer-boundary process crash is claimed. Migration 87 remains the destructive cutover boundary; schema 88 only adds the operation journal. Backup, signed candidate, deployment and browser acceptance remain open.
