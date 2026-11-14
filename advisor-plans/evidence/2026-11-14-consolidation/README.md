# Consolidation verification — 2026-11-14

This is a verification receipt, not another working plan. Remaining tasks belong
only in [CURRENT.md](../../CURRENT.md). See [verification.json](verification.json)
for results and limits, [publication.json](publication.json) for Git publication
status, and [source-files.sha256](source-files.sha256) for the tested file contents.

## Source and preservation

The implementation combines the shared Identity/Dex retirement work with the
retained runtime fixes. The older fixes checkout's HEAD tree exactly matched
the starting `origin/main` tree; its uncommitted non-evidence paths were compared
with the Identity tree. Intentional differences are the Identity replacement,
generated dependencies and formatting. All three dirty checkouts were privately
snapshotted before editing. Their original work remains recoverable.

The archive preserves 31 former top-level planning documents, their older
000–015 archive, runtime ledgers and evidence (1,461 files). The single working
plan carries 359 detailed acceptance requirements with unique identifiers;
these overlap by outcome and are not 359 independently estimated tasks.
The archive inventory retains original wording and its current identifier.
Historical schemas, counts, publication commands and completed work are explicitly
distinguished from the current candidate. Cancelled Dex-building work stays retired.

The catalog assurance branch is already merged into catalog main through PR #1.
Signed catalog publication and actual staging qualification remain open.

## Verification

- `make check`: lint, vet, formatting, four production binary builds and race
  tests pass; 15,568 total tests, seven explicit skips, zero failures/errors.
  [Skip inventory](go-skip-inventory.json) records prerequisites that still need
  qualification for a release; skips are not passing cases.
- Frontend type checking, lint, all 346 test files/2,408 tests, production build,
  CSP, bundle budgets, generated OpenAPI checks and three editor sanitizer/
  provenance tests pass. Formatting passed for 48 staged frontend files.
  Two test workers preserve existing timeouts while avoiding import contention.
- Real PostgreSQL tests verify Identity browser sessions, bindings, schema
  retirement, provisioning journal boundaries, claim recovery, audit atomicity,
  locking, catalog state projection and authenticated backup/restore to schema 88.
  The six provisioning cases include actual Kubernetes credential isolation;
  the owned temporary namespaces were removed.
- The real PostgreSQL/Redis production router matches all 894 permission rows.
  Read-only callers are denied on 243 mutation routes with 243 audit rows;
  administrator admission is verified on 793 non-public routes.
- Helm lint/render and the Go builder digest check pass.
- The configured gosec gate passes. Govulncheck reports no called or imported
  package vulnerabilities; three unused required-module advisories remain listed
  in the JSON receipt. The fresh npm audit reports zero vulnerabilities.
- The compressed historical evidence was scanned to archive depth three, with
  zero findings. Exact hash/prose/commit false-positive rules do not exclude the
  archive. Negative controls confirm that ordinary and 64-hex credential values
  are still detected in an allowed source-inventory path.

Commands used Go 1.27.4 through `mise exec`, `GOFLAGS=-p=4` and the private
root-filesystem temporary directory. The Go gate used the owned bridge fixture
address through `ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP`; no capacity guard was
weakened. Source hashes were compared after the final checks. The full branch
history is scanned again after committing and before pushing.

Integration selections were `Identity|SharedIdentity|TestVersionsAreContiguous|TestUp`
for Identity/config/handler/database/migrations; all provisioning tests with the
explicit owned kubeconfig; the catalog projection and disposable restore tests;
and the route-table and read-only/administrator authorization tests. This receipt
does not represent a run of every integration test or the full live browser suite.

## Findings resolved before publication

Three journal G115 findings were fixed with the existing checked conversion helper
while retaining the explicit upper-bound rejection. A real database test proves
acceptance at MaxInt64 and rejection immediately above it.

The initial fresh Go scan found GO-2026-6859 in Go 1.27.3. The application, tool
modules and builder image were moved to the fixed Go 1.27.4 patch and requalified.
The initial npm audit found a redirect-library advisory and a lint dependency
advisory. `follow-redirects` now resolves to 1.16.3; only the TypeScript parser's
`minimatch` is overridden to 11.0.1. The before/after audit reports are retained;
the exact upstream references and override removal condition are in
[security-exceptions.md](../../../docs/security-exceptions.md).

Logs preserve test output, with disposable database URLs, public Helm fixture
keys and NUL characters redacted or escaped. Public test-principal and historical
resource UUID fields that triggered generic-key heuristics are replaced with
labeled markers in three publication copies; [the sanitization receipt](evidence-sanitization.json)
records original and published digests. The originals remain in private snapshots. No production credentials were used
as fixtures. Raw local outputs are retained privately with the session snapshots. Historical
transcripts retain their original whitespace; whitespace checks cover source and
current prose, excluding retained archive/evidence output.

This work publishes source. It does not publish a release, apply schema 88 to a
live management database, remove live broker resources, or close S1–S7 acceptance.
