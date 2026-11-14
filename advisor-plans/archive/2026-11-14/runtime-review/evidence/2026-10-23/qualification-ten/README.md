> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Qualification prerequisites: next ten — 2026-10-23

Candidate checkout: `/root/work/astronomer-fixes`, branch
`fix/2.1-runtime-verification`. Extends Plan 032 §20.9. All prior remediation
edits are preserved. No commit, push, release, deployment or cluster deletion.

| Task | Implemented result |
|---|---|
| 1. Kubernetes contract | Delivery and preflight share `SupportsKubernetes`: stable 1.33–1.35, full versions required; build metadata accepted. Unknown, old, prerelease and malformed inputs reject. |
| 2. Read-only port | `scripts/qualify-offerings` provides inventory and preflight only. No mutation executors, run mode or legacy verify mode. Six registries and the retained 174-case manifest are available. |
| 3. Namespace identity | Namespace uniqueness is cluster-scoped; the same namespace on independent clusters is permitted. Repeated names/clusters/projects and incomplete explicit target contracts reject. |
| 4. Protected namespaces | default, kube-*, astronomer* and flux-* cannot become qualification targets. |
| 5. Task ownership | Require a configured task ID, attestation scope bound to project/namespace, current project membership, Active namespace and matching qualification label. Runner never creates/labels resources. |
| 6. Independent hosts | Hash-pinned operator infrastructure attestation must be fresh and complete. Shared host IDs, duplicate system UUIDs, missing/extra nodes and live node/ownership drift reject. API node identities corroborate the mapping. Hashes do not authenticate the operator or prove the underlying mapping; this trust boundary is explicit. |
| 7. Candidate binding | Origin, commit and task must match; dirty source blocks preflight and source identity is checked again after reads. Worktree-root hashing includes untracked source outside the invocation subdirectory. v2 report explicitly states lifecycle_qualified=false. |
| 8. Read/report boundaries | GET-only transport, no redirects, bounded files/HTTP bodies/pages/rows, ten-minute CLI deadline, private token files, shared strict JSON decoder, same-resource pagination, and atomic non-overwriting report publication. |
| 9. Regression coverage | Positive/negative race tests cover version, scope, ownership, host mappings, source binding, HTTP/token/report boundaries, pagination and closed report schema. Focused lint/vet pass; broad gate recorded separately below. |
| 10. Live preflight and plans | Fresh real login, six-registry inventory, preflight block, current cluster versions and node identities captured. Existing master and related plans reconciled. |

## Live results

`live-inventory-verified.json`: all **six registries PASS**, all **174 lifecycle
cases NOT_RUN**. An initial inventory got HTTP 401 from an expired token; a fresh
real login restored the supported read path. Token remained in a private file
and is absent from report bodies. T3 browser could not launch its root sandbox;
the repository Playwright workflow handled login after explicit preview-open
failure.

`live-preflight-verified.json`: **BLOCKED**, as intended:

- only one explicit member target;
- target uses a protected namespace;
- no task-specific qualification ID/attestation;
- source candidate is dirty.

The private config was not rewritten to manufacture ownership or independence.
`live-cluster-versions.json` independently confirms replacement and site-demo are
active/registered on **v1.35.7+k3s1**; old ui-demo remains **v1.30.4+k3s1**.

`live-node-identities.json` shows both replacement nodes and the site-demo node
report **the same system UUID**, `263c3b45-9849-48df-a062-cc25475d2584`. These
current targets fail the new identity-separation check. Distinct cluster IDs and
Ready nodes do not establish independent hosts. No infrastructure attestation
was invented from the node names.

## Remaining acceptance work

1. Obtain genuinely independent member infrastructure and reviewed host mapping.
2. Create dedicated task-owned project/namespace targets through supported owners;
   retain existing site/default workloads.
3. Freeze and sign an immutable candidate, verify deployed image identities, then
   run the new preflight against that exact candidate/estate.
4. Port reviewed mutation executors with bounded cleanup and durable operation
   evidence; execute the 174 cases and system failover/progress-deadline checks.
5. Preserve original ui-demo until replacement, migration and runtime acceptance.

Other open plans retain their pagination causal diagnosis, queued Dex
rollout/provider coverage, Charlie suspension, and recovery qualification work.
This batch implements the runner prerequisites; it does not close those tasks.

Usage, evidence format, trust boundaries and limits:
[`scripts/qualify-offerings/README.md`](../../../../../../../scripts/qualify-offerings/README.md).

## Final verification

- `make-check-final.log`: exit 0; lint/vet/format/four production builds/race pass.
  **15,758 total / 15,727 passed / 31 skipped / zero failures or errors**.
  Skip dispositions remain those documented in the preceding follow-up batch.
- `tests-hardened.log`, `runner-snapshot-test.log`, `lint-hardened.log`,
  `vet-final.log`: focused runner and compatibility regression checks pass.
- `security.log`: exit 0; zero gosec findings, zero reachable vulnerabilities,
  one module-only advisory, and no leaks across 2,304 branch-history commits.
  `runner-secrets.log`: new runner working-tree files also scan clean.
- `live-report-schema.log`: actual inventory and blocked preflight reports
  validate against their closed JSON schemas.
- All **86 source hashes match** `source-manifest.json`. All 74 files from the
  previous remediation manifest are unchanged. The production change outside
  the new tooling is the shared strict Kubernetes-version validator.
- The first broad run in `make-check.log` failed compilation because a new JSON
  dependency was added after Go captured its import graph (`open : no such file
  or directory`). Edits stopped; the runner snapshot passed independently and
  the complete frozen-source gate then passed. The failed run is retained.

Exact machine-readable results: `verification-summary.json`. The only edit after
Go-source freeze was a README wording correction, included in final hashes.
