> **Archived 2026-11-14. Historical record only.** See [the sole current working plan](../../../../../../CURRENT.md). This document does not authorize new work or certify the current candidate.

# Retired Dex contracts: semantic mapping

The focused handler/worker suite and real PostgreSQL advisory-lock integration
pass (see dex-charlie.log and dex-postgres.log). This is not live Dex rollout
qualification. The eight skipped tests remain until all their semantics have
active replacements; removing skips solely to improve the count would hide gaps.

| Retired test | Active replacement / remaining gap |
|---|---|
| Apply_PatchesMetadataOnlyRuntimeSecretAndRollsByResourceVersion | ApplyRuntimeSecret_ReconcilesOwnedMetadataAndPreservesForeignFields; DexGenerationCASStopsAfterInterleavedExternalReads. Worker-to-real-workload rollout remains open. |
| Apply_FixedPointDoesNotMutateOrRollout | DexOperationRuntimeSuccessIsTerminalAndDuplicateSafe proves terminal duplicate dispatch, but not a new operation at the same config fixed point. Add no-secret-write/no-rollout assertion for a fresh duplicate generation. |
| Apply_503WhenNoK8sRequester | DexApplyQueuesDurablyWithoutKubernetesAndReplaysExactlyOnce proves the new 202 + Location + Retry-After contract and exactly one queue/audit record; 503 is obsolete. |
| ConnectorMutationApplyRestoresOnlyPreviouslyEnabledSSO | ConnectorMutationFailureAndManualDisableRemainFailClosed; DexSagaAtomicFailureAndABRestorationTimeline; real DB enable/restore generation locks. Worker success after connector mutation still needs full operation-to-provider restoration proof. |
| RegisterAsSSO_CreatesProviderRow | Generation-CAS, saga and DB locks protect mutation, but they do not alone prove queued registration creates/enables the correct provider end to end. Keep gap. |
| RegisterAsSSO_AtomicFailurePreservesCredentialPair | DexSagaAtomicFailureAndABRestorationTimeline and DB connector_failure_is_atomic cover durable atomicity; retain queued registration credential-pair fault injection as a distinct task. |
| RegisterAsSSORolloutFailureLeavesProviderDisabledAndRetryConverges | DexOperationRuntimePersistsRetryWithoutLeakingCause covers safe retry; ConnectorMutationFailureAndManualDisableRemainFailClosed covers provider fail-closed. Combined real rollout failure → retry → provider convergence remains open. |
| RegisterAsSSOPlatformConfigFailurePreservesPair | RegisterAsSSORejectsEmptyPlatformURLMalformedEnvelopeAndDecryptFailure covers bounded errors before mutation. Add exact pair readback through the queued operation for platform-setting read failure. |

Next implementation: use actual operation dispatch and PostgreSQL; assert exact
provider/secret generation before and after injected rollout failure, retry,
stale generation, and unchanged fixed point. Delete obsolete bodies only with
those replacement names linked in the removal change.
