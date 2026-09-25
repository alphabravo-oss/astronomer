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

| Registry family | Result | Observed |
| --- | --- | --- |
| Feature flags | PASS | 12 exact keys; extensions, hosted Loki and control-plane snapshots disabled in this profile |
| Curated applications | PASS | 21 exact slugs |
| Tools | PASS | 12 exact slugs |
| Dex connector types | PASS | 10 exact connector types |
| Cloud-credential providers | PASS | AWS, Azure, DigitalOcean, GCP and Generic |
| Extensions | PASS for disabled profile | Public route returned 404 while `feature.extensions=false`, matching the explicit fail-closed contract |

The schema-valid local evidence is ignored under
`test-artifacts/offering-qualification/2026-09-25-phase0/inventory.json`; SHA-256
`fd420159898b2c5b86baa95be7db2da65e6b25c3aad4a2ad25cc0cc409af3380`.
The summary is 6 inventory PASS, 0 inventory FAIL, and 174 functional NOT_RUN.
Running comprehensive `verify` against it fails as required.

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
