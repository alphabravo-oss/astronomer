> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Shared Identity provisioning foundations — 2026-10-24

Implemented encrypted immutable credential storage satisfying published Identity v0.1.0 `pkg/provision.SecretStore`, with scoped ownership, idempotent concurrent writes, conflict rejection and UID/resource-version-guarded cleanup. Added a SQLC-generated PostgreSQL transaction advisory lock shared across all generations of an installation. These are foundations; catalog install/worker composition does not invoke them yet.

## Verification

- `go test -race -tags integration ./internal/identity/provisioning -identity-test-kubeconfig=/root/.kube/astronomer-ui-demo-v211.yaml -count=1 -v`: both tests pass, zero skips. Real Kubernetes v1.35.7+k3s1 and PostgreSQL 17.11. Temporary namespaces only; final labeled namespace inventory empty.
- `go test -race -tags integration ./internal/identity/...`: both packages pass. The cluster test deliberately skips without its explicit kubeconfig flag; the preceding dedicated run executes it against the real cluster.
- Targeted golangci-lint: zero issues. Targeted gosec: success, no findings. Targeted govulncheck: no called or imported-package vulnerabilities; one unused required-module advisory.
- `go build ./cmd/...`: success for all composition roots.
- `make sqlc`: regenerated query and interface, no hand edits.
- `go mod tidy` followed by `go mod tidy -diff`: clean. `tidy.log` retains the earlier dependency normalization diff, resolved before final source hashes.
- `git diff --check`: success.

The integration log includes disposable PostgreSQL fixture connection details, not deployment credentials. Source hashes cover this batch, not a new full-repository qualification. The prior full source/security/frontend gate belongs to the 2026-10-23 source snapshot and is not asserted for this new snapshot.

## Limits and next work

No upstream client was provisioned, no application release was changed, and no Dex workload was removed by these tests. The local-login path remains independent of this package. Storage is additionally encrypted with the product keyring; it is not directly consumable by a Helm application. Credential and key backups are jointly required.

An advisory lock is not crash-safe distributed provisioning. Durable operation intent, ambiguous-result recovery, audited authorization, worker/API wiring, per-product Secret delivery, upstream lifecycle APIs, rotation/revocation and second-product browser proof remain open in Plan 032 §20.13 tasks 6.3–6.10. The shared v0.1.0 API has no application rotation/deletion method. Never retry upstream creation merely because a secret is missing; never erase a credential to undo a timed-out write.
