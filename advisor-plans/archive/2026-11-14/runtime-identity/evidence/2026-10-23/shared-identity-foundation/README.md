> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Shared Identity verification foundation — 2026-10-23

Plan 032 §20.13 continuation, branch `feat/identity-zitadel`, base
`040870752fc1635ecbb08d64f9e4b71538a061f4`. The shared RP module is pinned to
`github.com/alphabravo-oss/identity v0.1.0` without a local replacement.

Local-only and local-plus-SSO are the only supported configuration modes. This
slice implements lazy shared JWT verification and explicit issuer/subject account
bindings. It does not yet wire browser login or replace existing server routes.
Matching an email is never sufficient for a binding; upstream roles do not confer
local administrator status. Each authentication checks current PostgreSQL state,
including deactivation, unlinking, service-account exclusion and force logout.

Migration 86 is additive and updates the chart's expected schema metadata. The
previous discarded Dex migration 86 was uncommitted and was not reused. No live
migration, cluster changes, image publication or deployment occurred.

## Verification

- `integration.log`: `go test -race -tags integration ./internal/identity -count=1`.
  Production migrations run on real PostgreSQL 17.11. A real TLS JWKS endpoint and
  RSA-signed tokens exercise issuer/audience/expiry/algorithm boundaries, duplicate
  binding ownership, same subjects across issuers, 16 concurrent initial requests,
  cached keys during issuer outage, account revocation and cold-start SSO failure.
  This fixture is not a deployed Identity/cross-product SSO acceptance test.
- Focused lint: `golangci-lint run ./internal/identity/... ./internal/config/...`.
- `check.log`: full `make check` passed: 15,769 tests reported, 15 skipped,
  297.995 seconds for the test stage. Skipped legacy Dex tests are not acceptance
  or retirement proof. Lint, vet, formatting and all four binary builds passed.
- `security.log`: `make security` passed. Govulncheck reports no reachable or
  imported-package vulnerabilities; it records the uncalled required-module
  advisory GO-2026-5932 for legacy `golang.org/x/crypto/openpgp`. Gitleaks scanned
  2,304 branch-history commits with no leaks. This history scan does not itself
  certify uncommitted files.
- `go mod tidy -diff` and `git diff --check` pass.

`receipt.json` records the source hashes and gate results.
`dex-retirement-inventory.json` is a source-reference inventory for review, not a
list to delete automatically. It separates historical migrations, generated/query
contracts, runtime, frontend, chart and documentation. Mixed local-auth/RBAC code
must be preserved; historical migrations are superseded by forward migrations.

Next: shared browser PKCE/state flow, secure cookie/CSRF and logout lifecycle,
authenticated/audited binding administration, chart Secret/private-CA configuration,
settings/catalog provisioning, then Dex code/data retirement and full live proof.
