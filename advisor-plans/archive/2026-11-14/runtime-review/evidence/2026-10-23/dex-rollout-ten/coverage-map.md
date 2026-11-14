> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Dex rollout coverage reconciliation

**SUPERSEDED:** follow-up gaps below are historical, cancelled as target scope by
Plan 032 §20.13 (shared Identity adoption and Dex retirement). They are not active
release blockers. Retain the observations for selective cleanup of the old code.

This supersedes the gap assessment in followup-ten/dex-coverage-map.md without
rewriting historical evidence. Seven old synchronous test bodies remain skipped;
they are not counted as passing. The live test dispatches the production worker
handler with real PostgreSQL and a TLS Kubernetes API client. It does not exercise
Redis delivery, the agent tunnel, external IdP login or the deployed management plane.

| Legacy scenario | Current evidence and remaining work |
|---|---|
| Apply metadata repair and resource-version rollout | Real `foreign_secret_fields_survive_metadata_repair` plus pure readiness checks; foreign Secret data and metadata preserved. |
| Fresh fixed-point apply | Real `fresh_apply_fixed_point`: new admitted operation, unchanged Secret resourceVersion and Deployment generation, zero requester writes. |
| Restore only previously enabled SSO | Real `connector_mutation_restores_enabled_provider`, `manual_disable_survives_apply` and failing-rollout/retry lifecycle. |
| Register creates provider | Real registration rotates/enables an existing fixture provider with matching secret and applied generation. First registration with no provider row and repeated credential rotation remain distinct follow-ups. |
| Registration atomic failure | Prior real PostgreSQL `TestDexRegistrationFailuresPreserveDurablePair` covers platform/provider read and staging/audit rollback. Keep finalization-stage fault injection and exact runtime/provider pair readback as follow-ups. |
| Registration rollout failure then retry | Real failure/retry is proved for connector-mutation Apply. Registration-specific failure with a newly rotated secret still needs its own real scenario. |
| Platform config failure preserves pair | Prior real PostgreSQL failure matrix checks unchanged encrypted settings/provider state and no operation/outbox/audit admission. |

The stale-operation scenario additionally proves no Kubernetes writes after a
newer settings generation supersedes the admitted operation.

No legacy body is removed in this batch: partial overlap does not establish every
assertion in an obsolete synchronous flow. Remaining cases above should be added
to the real lifecycle harness before deleting their historical bodies.
