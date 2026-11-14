> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Qualification runner review and implementation sequence

Historical source is `/root/astronomer-all/astronomer/scripts/qualify-offerings`.
It is absent from the candidate checkout; copying it unchanged would restore
obsolete acceptance criteria. Existing inventory/preflight evidence remains
read-only; all 174 lifecycle cases remain NOT_RUN.

Confirmed source gaps:

- `preflight.go:clusterPreflightResponse` does not decode kubernetes_version.
  `inspectMemberTarget` can accept old ui-demo 1.30 if other fields look ready.
  Add a supported version gate aligned with current
  `internal/delivery/compatibility/compatibility.go` (1.33–1.35), rejecting empty,
  malformed, unsupported and prerelease versions; test K3s build suffixes.
- `validateMemberTargetConfig` establishes unique cluster UUIDs, not independent
  hosts. Add verified node/provider host identities and a captured infrastructure
  mapping; reject shared hosts and absent provenance. Two cluster IDs or two
  node names are not sufficient evidence of independent recovery domains.
- Namespace uniqueness is global rather than (cluster, namespace). Preserve
  task ownership while correcting that identity. Require dedicated owned project
  and namespace, with explicit cleanup scope; default/system namespaces are not
  acceptable qualification fixtures merely because they exist.
- Source identity must bind an immutable clean candidate and deployed image
  digests/signatures. Keep the dirty-source block. Port read-only inventory and
  preflight before bringing forward mutation executors; test negative gates
  before allowing any lifecycle execution.

Ordered tasks:
1. Extract current release compatibility input into a reusable validator and
   test supported/unsupported/unknown versions against the release contract.
2. Port GET-only runner/model/inventory and version gate into candidate source.
3. Add independent-host evidence format, digest binding, freshness and negative
   fixtures; obtain actual host mapping for site-demo/replacement.
4. Create dedicated task-owned project/namespace targets via supported APIs;
   verify exact scope without editing existing site workloads.
5. Bind clean candidate, six immutable images and signing identity; run preflight.
6. Port mutation executors with bounded cleanup, operation polling and durable
   failure capture; validate on owned fixtures before running 174 cases.
7. Run system failover/progress deadline and app lifecycle acceptance; retain old
   ui-demo until data migration and replacement acceptance are explicit.

No historic PASS is promoted to current qualification. The live estate and
release remain blockers. This turn reviewed and specified the port; it did not
implement or execute mutation tooling.

Cross-plan reconciliation: 016 remains the release/GA gate; 026 retains runtime
workflow and Charlie closure; 028/029 retain the full offering matrix and estate
acceptance; 030 retains pagination causal diagnosis; 033 retains application/tool
assurance. None is superseded by the local sanitizer or private-fixture passes.
