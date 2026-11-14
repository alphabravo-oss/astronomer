> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Shared Identity integration and Dex source retirement — 2026-10-23

Implemented in `/root/work/astronomer-identity`, branch `feat/identity-zitadel`,
base `040870752fc1635ecbb08d64f9e4b71538a061f4`. Changes are uncommitted and
undeployed. Shared relying party: `github.com/alphabravo-oss/identity` v0.1.0.

Local login is always available. Optional SSO accepts one configured trusted OIDC
issuer through the shared code/PKCE/state/JWKS verifier. Audited explicit
issuer/subject links resolve existing product accounts. Astronomer owns RBAC and
issues a bounded local access cookie; it never forwards the provider credential
to the browser. Binding removal, account disablement and session cutoff are
checked on every identity-session validation. Local-only mode has no provider
Secret/CA dependency. Production no longer requires the retired password-only
acknowledgment.

Dex routes, worker/recovery registration, renderer, bootstrap, validator command,
frontend install/connector/Apply clients, chart templates/values, runtime image
inventory and signature-policy exception are removed. Historical migrations and
historical release records remain. Generic OIDC, local authentication and the
existing independent SCIM/group facilities remain; shared login does not import
upstream groups or grant privileges.

Migration 87 removes the retired broker tables/contracts and pending broker
outbox deliveries. Before removing provenance it invalidates existing sessions
for broker-linked accounts, revokes their refresh families, and records old Dex
session JTIs as revoked. Local credentials, MFA/recovery and roles survive;
unrelated local accounts are unaffected. Old subjects are never automatically
relinked at a new issuer.

## Verification

- `TMPDIR=/root/work/astronomer-test-tmp ASTRONOMER_HELM_HTTP_PRIVATE_FIXTURE_IP=172.17.0.1 make check`: **PASS**, lint/vet/format/four binary builds/race tests. **15,568 tests reported; 7 skipped; no failures**. See `make-check.log` and `unit.xml`.
- Real PostgreSQL 17.11 and TLS issuer tests: **PASS** (`integration.log`). Covers fresh schema and 86→87 upgrade, preserved local security rows, old broker session retirement, PKCE/state, exact bindings, wrong issuer/audience, transaction rollback/conflict/audit, bounded local cookies, unlink denial and logout.
- Private-CA trust/local independence, generated SDK provider listing and current session-mint contract: **PASS** (`private-ca-and-session-contract.log`).
- Frontend: **346 files / 2,408 tests PASS, zero skips**, typecheck/lint/format/build PASS. Final tests used `--maxWorkers=2`; deadlines and assertions were unchanged. Generated browser OpenAPI checks pass.
- `make security`: **PASS**, zero gosec findings, zero called or imported-package vulnerabilities. One required-module advisory remains in unused code. Full-history gitleaks passes. See `security.log` and SARIF files.
- Supplemental working-source scan found 17 pre-existing development-fixture/constant findings, all on source lines identical to HEAD; no newly introduced finding. Raw directory scanning is not claimed green. `source-scan-disposition.json` records locations and line hashes without credential values.
- Browser smoke: local password form visible and no Dex content (`login.png`). This is a login-page rendering check, not a live authenticated SSO journey. T3 preview failed with `Chrome will not start its sandbox as root`; repository Playwright tooling was used against an owned local Vite server, then stopped.
- `retirement-inventory.json`: all source/API/chart/image absence checks true. `source-sha256.json` freezes **5,125** source files; all hashes remained unchanged through the final gate.

Seven remaining Go skips require external prerequisites: two PromQL checks, one
pinned monitoring-chart render and four selected-Cosign proofs. Retired Dex tests
were removed, not converted into passing product-qualification cases.

Earlier failures are retained: a stale callback contract was corrected to cover
the new context-aware identity mint; private-CA behavior was added to real TLS
coverage. Upgrade tests correctly refused artifact work below their maintained
capacity reserve. We removed 770 obsolete Astronomer cache entries (12.09 GiB),
excluding current normal/race exports; no capacity guard was relaxed and no
source/database/image/volume was deleted. The upgrade package and complete gate
then passed. The formatter now handles deleted and untracked Go sources.
Frontend contention caused two lazy-module hook timeouts on the concurrent run;
reduced worker concurrency passed all cases without raising timeouts.

## Remaining acceptance work

No live database migration, workload uninstall, deployment, release publication
or cross-product qualification has run. Follow the cutover runbook at
`docs/runbooks/shared-identity-cutover.md`: verified backup, local administrator
recovery proof, explicit identity mapping, old-worker draining, and owned cleanup
of Helm-retained or separately installed broker resources are mandatory before
production cutover. Old binaries cannot run against schema 87.

Shared catalog client provisioning, provider-group role synchronization, global
logout, LDAP/SAML through the upstream provider, second-product SSO, private-CA
and air-gap deployment qualification, release signatures and live cluster
cleanup remain explicit Plan 032 acceptance work. A successful component/source
test does not close those product or estate gates.
