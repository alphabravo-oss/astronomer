# Plan 028 execution ledger

## 2026-09-25 — harness and preliminary Phase 0 inventory

Status: **IN PROGRESS**. This checkpoint begins implementation and records a
read-only live inventory. It does not claim that any application, Tool,
provider, extension, lifecycle action or functional canary is qualified.

### Implemented in this checkpoint

- A machine denominator containing all 174 case IDs from the companion test
  matrix, plus runtime contracts for feature flags, catalog applications,
  Tools, Dex connector types, cloud-credential providers and extensions.
- A GET-only Go inventory runner with strict configuration decoding, an
  owner-only credential-file requirement, same-origin bounded pagination,
  response-size and request-time limits, frozen Git state hashes and atomic
  evidence writes.
- A comprehensive evidence verifier that rejects inventory-only reports,
  dirty candidates, missing or duplicate cases, FAIL/BLOCKED/NOT_RUN states,
  uncontracted NOT_SUPPORTED states, unmatched registries, failed cleanup and
  inconsistent summaries.
- A closed JSON evidence schema, offline false-green tests, a PR static gate
  and a protected manual release-candidate inventory workflow.
- A GET-only mutation-estate preflight that refuses the management cluster,
  duplicate or implicit targets, stale/disconnected agents, privilege-profile
  drift, and projects that do not own the configured namespace. Its closed
  evidence records PASS or BLOCKED without exposing credentials.

### Live inventory result

The read-only run used the normal login and API-token endpoints. The temporary
one-day token was revoked through its public API immediately after the run;
the token and login material were removed from disk. No application, Tool,
setting or cluster resource was changed.

The target was the existing development management plane at
`https://astronomer.dev.alphabravo.io`, chart `astronomer-1.2.0` revision 140,
on K3s `v1.35.7+k3s1`/amd64. The runner recorded source base commit
`1975a32b622b3b094235b8d7ed01a5606a76c530` plus staged, unstaged and untracked
hashes because this was a development checkpoint, not immutable release
evidence. The local catalog was clean at
`3e24ab98f0805bd54d6591d9b9b961bbfd1f5db5`, digest
`084aa9dec47d9612fdebd848737f53f9feb70e248a4e8e5ccbc93491e1f4ae0c`.

| Registry family            | Result                    | Observed                                                                                               |
| -------------------------- | ------------------------- | ------------------------------------------------------------------------------------------------------ |
| Feature flags              | PASS                      | 12 exact keys; extensions, hosted Loki and control-plane snapshots disabled in this profile            |
| Curated applications       | PASS                      | 21 exact slugs                                                                                         |
| Tools                      | PASS                      | 12 exact slugs                                                                                         |
| Dex connector types        | PASS                      | 10 exact connector types                                                                               |
| Cloud-credential providers | PASS                      | AWS, Azure, DigitalOcean, GCP and Generic                                                              |
| Extensions                 | PASS for disabled profile | Public route returned 404 while `feature.extensions=false`, matching the explicit fail-closed contract |

The schema-valid local evidence is ignored under
`test-artifacts/offering-qualification/2026-09-25-phase0/inventory.json`; SHA-256
`fd420159898b2c5b86baa95be7db2da65e6b25c3aad4a2ad25cc0cc409af3380`.
The summary is 6 inventory PASS, 0 inventory FAIL, and 174 functional NOT_RUN.
Running comprehensive `verify` against it fails as required.

### Live mutation-estate preflight

The API-backed preflight ran against clean candidate
`03ec3c1421e9ea34673d4430c253d4bdffba8c2f` and correctly returned **BLOCKED**.
The only real target is cluster `900db10f-b2cd-41e4-9603-0fa4d3fc8657`:
the API identified it as the local management cluster with the `viewer` agent
profile, so the runner refused it as an install target. The API returned 404
for the explicit second-member sentinel, confirming there is no second adopted
member available to satisfy the estate contract. No mutating request ran.

The temporary one-day API token was issued and revoked through the public API,
and its local material was removed. The ignored evidence is
`test-artifacts/offering-qualification/2026-09-25-estate-preflight/estate-preflight.json`;
SHA-256 `c853f712aca7e35e7b0137f2a69a39ae7049596c625aa1940bb9dafd47790cfc`.

### Defect found and corrected

The live feature endpoint returned `feature.alerting`, `feature.delivery` and
`feature.control_plane_snapshots`, but the OpenAPI `FeatureFlags` schema omitted
them. This checkpoint adds the three server-owned keys to the OpenAPI source and
regenerates the embedded specification, TypeScript types and Go client. The
inventory now fails if a future runtime feature key lacks a frozen contract.

### Required next execution

The current environment has only the management cluster and does not satisfy
the dedicated two-member test-estate prerequisite. The runner and protected
workflow now enforce this prerequisite before any mutating case. Create and
adopt task-owned member clusters through the documented environment and
registration flow, assign explicit projects/namespaces, freeze their IDs and
deploy an immutable build from this branch. Then expand executable runtime
reconciliation across the remaining registries and implement the dependency
DAG, lifecycle executors, functional canaries, durable readback and API-owned
cleanup. External provider accounts and destinations that are not configured
must remain BLOCKED while independent local lanes continue.

## 2026-09-25 — merged-main inventory and estate gate

Status: **BLOCKED before mutation**. Navigation and workflow changes through
PRs 29–40 are merged on clean `main` at
`9e74ea94664bdc82550778fd6471116c9794a966`. The frontend from that commit is
running on the development management plane as Helm revision 143, pinned to
`localhost/astronomer-frontend@sha256:84986d07dee5f94aacdebf2654a94fb173093b34d8ef4ecac041d7050ac8360b`.
Public `/healthz` returned 200 and server `/readyz` reported healthy database,
Redis, schema, critical runtime and tunnel checks.

This deployment remains a development checkpoint rather than a qualified
release candidate. Its server and worker images predate the frontend commit,
and the Helm release reports the explicit schema-skew development override.
No functional result from this checkpoint may be carried into release evidence.

The GET-only inventory ran through normal local login and one-day API-token
issuance. The token was revoked through the public API immediately afterward,
and its local credential material was destroyed. The six implemented registry
contracts all passed with the same denominator: 12 feature flags, 21 curated
applications, 12 Tools, 10 Dex connector types, five cloud-credential
providers and the disabled/fail-closed extensions contract. All 174 functional
cases remain `NOT_RUN`; an inventory pass is not an install pass. Evidence:

- `test-artifacts/offering-qualification/2026-09-25-main-merged/inventory.json`
- SHA-256 `617318e141a6b03edec6c7f92c11e6d5e2b677d9857ed22006326f0143a4ecc4`

The API-backed estate preflight then ran against the same clean commit and
returned `BLOCKED`. Cluster `900db10f-b2cd-41e4-9603-0fa4d3fc8657` is still
the local management cluster and exposes the `viewer` agent profile; it is not
an allowed install target. The explicit second-member sentinel returned 404,
so the estate still lacks the required two distinct, non-local, ready member
clusters with matching admin profiles, projects and namespaces. No mutating
request ran. Evidence:

- `test-artifacts/offering-qualification/2026-09-25-main-merged/estate-preflight.json`
- SHA-256 `b1b38217a341ddcbb57341b229d7411c0bb7616f2d73b1826ce846070a89c03a`

Harness verification passed with `go test ./scripts/qualify-offerings/...` and
all six offline schema/false-green tests. Functional install validation may
start only after an immutable, internally consistent candidate is deployed and
the preflight passes with two explicit disposable member targets. Supplying
those clusters through the normal adoption flow is an environment prerequisite;
using the management cluster, direct Helm installation, namespace aliases or
new K3d residue would violate the qualification contract.
