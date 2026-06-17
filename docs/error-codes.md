<!-- GENERATED FILE — DO NOT EDIT.
     Source: internal/handler/apierror/codes.go
     Regenerate: node scripts/error-code-docs.mjs --write
     CI checks freshness via: make error-codes-check -->

# API error codes

Every error response produced by the Astronomer REST API carries a stable,
machine-readable `code` string in its body:

```json
{"error": {"code": "<code>", "message": "<message>", "request_id": "..."}}
```

This document is generated from the canonical catalog in
[`internal/handler/apierror/codes.go`](../internal/handler/apierror/codes.go). Each row lists the Go constant,
the literal wire value clients actually observe (emitted verbatim from source —
never re-derived from the identifier), the HTTP status family the code typically
accompanies, and a short description. Codes are grouped by status family; a
handful of codes legitimately appear under more than one status depending on
context, so the grouping reflects the dominant usage, not an exhaustive contract.

**Total codes: 217**

## Codes by category

### Validation / bad client input

Dominant HTTP status: 400 · Provenance: seed

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `InvalidBody` | `invalid_body` | 400 | indicates the request body could not be decoded (malformed JSON or wrong shape). |
| `InvalidID` | `invalid_id` | 400 | indicates a path or query identifier failed to parse (e.g. a non-UUID id). |
| `ValidationError` | `validation_error` | 400 | indicates the request was well-formed but failed field-level validation rules. |
| `InvalidRequest` | `invalid_request` | 400 | indicates a generically malformed or unsatisfiable request that is not covered by a more specific validation code. |
| `InvalidName` | `invalid_name` | 400 | indicates a supplied name violates its naming constraints. |
| `InvalidToken` | `invalid_token` | 400 | indicates a supplied token is missing or malformed. (When used in an authentication context this typically accompanies a 401.) |
| `AccountDisabled` | `account_disabled` | 400 / 422 | indicates a account disabled condition. |
| `AuditRetentionDowngrade` | `audit_retention_downgrade` | 400 / 422 | indicates a audit retention downgrade condition. |
| `BaselineDisabled` | `baseline_disabled` | 400 / 422 | indicates a baseline disabled condition. |
| `BaselineRequired` | `baseline_required` | 400 / 422 | indicates a baseline required condition. |
| `BuiltinReadonly` | `builtin_readonly` | 400 / 422 | indicates a builtin readonly condition. |
| `BuiltinTemplate` | `builtin_template` | 400 / 422 | indicates a builtin template condition. |
| `ComponentInvalid` | `component_invalid` | 400 / 422 | indicates a component invalid condition. Aliases: `invalid_component` |
| `ComponentRequired` | `component_required` | 400 / 422 | indicates a component required condition. |
| `ImmutableName` | `immutable_name` | 400 / 422 | indicates a immutable name condition. |
| `ImmutableProvider` | `immutable_provider` | 400 / 422 | indicates a immutable provider condition. |
| `IncompatibleExtension` | `incompatible_extension` | 400 / 422 | indicates a incompatible extension condition. |
| `InvalidAddr` | `invalid_addr` | 400 / 422 | indicates a invalid addr condition. |
| `InvalidCIDR` | `invalid_cidr` | 400 / 422 | indicates a invalid cidr condition. |
| `InvalidChallenge` | `invalid_challenge` | 400 / 422 | indicates a invalid challenge condition. |
| `InvalidClusterID` | `invalid_cluster` | 400 / 422 | indicates a invalid cluster condition. NOTE: the wire value is "invalid_cluster" (NOT "invalid_cluster_id") — preserved from the pre-codemod literal at anomaly.go and argocd.go. The Go identifier keeps the ...ID suffix for catalog consistency with the other invalid_* id codes; do not assume the constant name equals the emitted string. |
| `InvalidCode` | `invalid_code` | 400 / 422 | indicates a invalid code condition. |
| `InvalidContext` | `invalid_context` | 400 / 422 | indicates a invalid context condition. |
| `InvalidDecision` | `invalid_decision` | 400 / 422 | indicates a invalid decision condition. |
| `InvalidEmail` | `invalid_email` | 400 / 422 | indicates a invalid email condition. |
| `InvalidExpiresAt` | `invalid_expires_at` | 400 / 422 | indicates a invalid expires at condition. |
| `InvalidField` | `invalid_field` | 400 / 422 | indicates a invalid field condition. |
| `InvalidFilter` | `invalid_filter` | 400 / 422 | indicates a invalid filter condition. |
| `InvalidFormat` | `invalid_format` | 400 / 422 | indicates a invalid format condition. |
| `InvalidGroupName` | `invalid_group_name` | 400 / 422 | indicates a invalid group name condition. |
| `InvalidKey` | `invalid_key` | 400 / 422 | indicates a invalid key condition. |
| `InvalidKind` | `invalid_kind` | 400 / 422 | indicates a invalid kind condition. |
| `InvalidManifest` | `invalid_manifest` | 400 / 422 | indicates a invalid manifest condition. |
| `InvalidMode` | `invalid_mode` | 400 / 422 | indicates a invalid mode condition. |
| `InvalidParent` | `invalid_parent` | 400 / 422 | indicates a invalid parent condition. |
| `InvalidPolicy` | `invalid_policy` | 400 / 422 | indicates a invalid policy condition. |
| `InvalidProvider` | `invalid_provider` | 400 / 422 | indicates a invalid provider condition. |
| `InvalidRange` | `invalid_range` | 400 / 422 | indicates a invalid range condition. |
| `InvalidResource` | `invalid_resource` | 400 / 422 | indicates a invalid resource condition. |
| `InvalidScope` | `invalid_scope` | 400 / 422 | indicates a invalid scope condition. |
| `InvalidScopeParams` | `invalid_scope_params` | 400 / 422 | indicates a invalid scope params condition. |
| `InvalidServiceProxyTarget` | `invalid_service_proxy_target` | 400 / 422 | indicates a invalid service proxy target condition. |
| `InvalidSince` | `invalid_since` | 400 / 422 | indicates a invalid since condition. Aliases: `since_invalid` |
| `InvalidStars` | `invalid_stars` | 400 / 422 | indicates a invalid stars condition. |
| `InvalidState` | `invalid_state` | 400 / 422 | indicates a invalid state condition. |
| `InvalidStatus` | `invalid_status` | 400 / 422 | indicates a invalid status condition. |
| `InvalidStep` | `invalid_step` | 400 / 422 | indicates a invalid step condition. |
| `InvalidTaint` | `invalid_taint` | 400 / 422 | indicates a invalid taint condition. |
| `InvalidTargetRefs` | `invalid_target_refs` | 400 / 422 | indicates a invalid target refs condition. |
| `InvalidType` | `invalid_type` | 400 / 422 | indicates a invalid type condition. |
| `InvalidURL` | `invalid_url` | 400 / 422 | indicates a invalid url condition. |
| `MaxDepth` | `max_depth` | 400 / 422 | indicates a max depth condition. |
| `MissingCluster` | `missing_cluster` | 400 / 422 | indicates a missing cluster condition. |
| `MissingIssuer` | `missing_issuer` | 400 / 422 | indicates a missing issuer condition. |
| `MissingParams` | `missing_params` | 400 / 422 | indicates a missing params condition. |
| `ModeChangeRequiresForce` | `mode_change_requires_force` | 400 / 422 | indicates a mode change requires force condition. |
| `NoDefault` | `no_default` | 400 / 422 | indicates a no default condition. |
| `NoDestination` | `no_destination` | 400 / 422 | indicates a no destination condition. |
| `NoSettings` | `no_settings` | 400 / 422 | indicates a no settings condition. |
| `NoSnapshot` | `no_snapshot` | 400 / 422 | indicates a no snapshot condition. |
| `NoteTooLong` | `note_too_long` | 400 / 422 | indicates a note too long condition. |
| `QueueRequired` | `queue_required` | 400 / 422 | indicates a queue required condition. |
| `ReasonRequired` | `reason_required` | 400 / 422 | indicates a reason required condition. |
| `SMTPDisabled` | `smtp_disabled` | 400 / 422 | indicates a smtp disabled condition. |
| `StaleDefault` | `stale_default` | 400 / 422 | indicates a stale default condition. |
| `StepMismatch` | `step_mismatch` | 400 / 422 | indicates a step mismatch condition. |
| `TemplateDisabled` | `template_disabled` | 400 / 422 | indicates a template disabled condition. |
| `UnknownKey` | `unknown_key` | 400 / 422 | indicates a unknown key condition. |
| `UnsafeReplacementBlocked` | `unsafe_replacement_blocked` | 400 / 422 | indicates a unsafe replacement blocked condition. |
| `UnsupportedProvider` | `unsupported_provider` | 400 / 422 | indicates a unsupported provider condition. |
| `UnsupportedType` | `unsupported_type` | 400 / 422 | indicates a unsupported type condition. |

### Not found

Dominant HTTP status: 404 · Provenance: seed

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `NotFound` | `not_found` | 404 | indicates the requested resource does not exist. Prefer this generic code over entity-specific variants (cluster_not_found, etc.). |

### Conflict / state and uniqueness violations

Dominant HTTP status: 409 · Provenance: seed

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `Conflict` | `conflict` | 409 | indicates the request conflicts with the current state of the resource (uniqueness violation, illegal state transition, etc.). |

### Authentication and authorization

Dominant HTTP status: 401 / 403 · Provenance: seed

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `AuthenticationRequired` | `authentication_required` | 401 / 403 | indicates the caller is not authenticated and a credential is required (HTTP 401). |
| `Forbidden` | `forbidden` | 401 / 403 | indicates the caller is authenticated but lacks permission for the requested operation (HTTP 403). |
| `ScopeDenied` | `scope_denied` | 401 / 403 | indicates the caller's credential (e.g. an API token) is missing a required OAuth-style scope, distinct from an RBAC permission denial. Distinguishes "your token can't do this" from "you can't do this" (HTTP 403). Not collapsed into Forbidden: clients branch on it to prompt for a re-scoped token rather than an access request. |

### Server / IO / database failures

Dominant HTTP status: 500 · Provenance: seed

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `InternalError` | `internal_error` | 500 | indicates an unexpected server-side failure with no more specific classification. |
| `DBError` | `db_error` | 500 | indicates a database query or transaction failed. |
| `ListError` | `list_error` | 500 | indicates a list/read query backing a collection endpoint failed. |
| `CountError` | `count_error` | 500 | indicates a count query backing a paginated endpoint failed. |
| `CreateError` | `create_error` | 500 | indicates a resource could not be created due to a server-side failure. |
| `UpdateError` | `update_error` | 500 | indicates a resource could not be updated due to a server-side failure. |
| `DeleteError` | `delete_error` | 500 | indicates a resource could not be deleted due to a server-side failure. |

### Authentication, SSO, MFA and token errors

Dominant HTTP status: 401 · Provenance: codemod

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `RecoveryFailed` | `recovery_failed` | 401 | indicates a recovery failed condition. |
| `SSOCallbackError` | `sso_callback_error` | 401 | indicates a sso callback error condition. |
| `SSOError` | `sso_error` | 401 | indicates a sso error condition. |
| `SSOInvalidRequest` | `sso_invalid_request` | 401 | indicates a sso invalid request condition. |
| `SSOInvalidState` | `sso_invalid_state` | 401 | indicates a sso invalid state condition. |
| `SSOMissingEmail` | `sso_missing_email` | 401 | indicates a sso missing email condition. |
| `SSONotConfigured` | `sso_not_configured` | 401 | indicates a sso not configured condition. |
| `SSOProviderError` | `sso_provider_error` | 401 | indicates a sso provider error condition. |
| `SSOUserError` | `sso_user_error` | 401 | indicates a sso user error condition. |
| `TOTPEncryptFailed` | `totp_encrypt_failed` | 401 | indicates a totp encrypt failed condition. |
| `TOTPGenerateFailed` | `totp_generate_failed` | 401 | indicates a totp generate failed condition. |
| `TokenError` | `token_error` | 401 | indicates a token error condition. |
| `TokenGenerationError` | `token_generation_error` | 401 | indicates a token generation error condition. |

### Authorization / access control

Dominant HTTP status: 403 · Provenance: codemod

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `AccountLocked` | `account_locked` | 403 | indicates a account locked condition. |
| `CSRFRequired` | `csrf_required` | 403 | indicates a csrf required condition. |
| `ServiceProxyDenied` | `service_proxy_denied` | 403 | indicates a service proxy denied condition. |
| `WrongClusterScope` | `wrong_cluster_scope` | 403 | indicates a wrong cluster scope condition. |

### Dependency unavailable, not configured, or not wired

Dominant HTTP status: 409 / 412 / 500 / 503 · Provenance: codemod

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `AgentFleetUnavailable` | `agent_fleet_unavailable` | 409 / 412 / 500 / 503 | indicates a agent fleet unavailable condition. |
| `CatalogUnavailable` | `catalog_unavailable` | 409 / 412 / 500 / 503 | indicates a catalog unavailable condition. |
| `DBUnavailable` | `db_unavailable` | 409 / 412 / 500 / 503 | indicates a db unavailable condition. |
| `DetectorUnwired` | `detector_unwired` | 409 / 412 / 500 / 503 | indicates a detector unwired condition. |
| `DiffUnavailable` | `diff_unavailable` | 409 / 412 / 500 / 503 | indicates a diff unavailable condition. |
| `DispatcherUnavailable` | `dispatcher_unavailable` | 409 / 412 / 500 / 503 | indicates a dispatcher unavailable condition. |
| `EncryptUnavailable` | `encrypt_unavailable` | 409 / 412 / 500 / 503 | indicates a encrypt unavailable condition. |
| `HistoryUnavailable` | `history_unavailable` | 409 / 412 / 500 / 503 | indicates a history unavailable condition. |
| `InspectorUnavailable` | `inspector_unavailable` | 409 / 412 / 500 / 503 | indicates a inspector unavailable condition. |
| `LogsUnavailable` | `logs_unavailable` | 409 / 412 / 500 / 503 | indicates a logs unavailable condition. |
| `NotCancellable` | `not_cancellable` | 409 / 412 / 500 / 503 | indicates a not cancellable condition. |
| `NotConfigured` | `not_configured` | 409 / 412 / 500 / 503 | indicates a not configured condition. |
| `NotEnrolled` | `not_enrolled` | 409 / 412 / 500 / 503 | indicates a not enrolled condition. |
| `NotFailed` | `not_failed` | 409 / 412 / 500 / 503 | indicates a not failed condition. |
| `NotImplemented` | `not_implemented` | 409 / 412 / 500 / 503 | indicates a not implemented condition. |
| `NotWired` | `not_wired` | 409 / 412 / 500 / 503 | indicates a not wired condition. |
| `PlanInUse` | `plan_in_use` | 409 / 412 / 500 / 503 | indicates a plan in use condition. |
| `PlanIsReserved` | `plan_is_reserved` | 409 / 412 / 500 / 503 | indicates a plan is reserved condition. |
| `RBACUnavailable` | `rbac_unavailable` | 409 / 412 / 500 / 503 | indicates a rbac unavailable condition. |
| `RunnerUnwired` | `runner_unwired` | 409 / 412 / 500 / 503 | indicates a runner unwired condition. |
| `SSOUnavailable` | `sso_unavailable` | 409 / 412 / 500 / 503 | indicates a sso unavailable condition. |
| `SearchUnavailable` | `search_unavailable` | 409 / 412 / 500 / 503 | indicates a search unavailable condition. |
| `ShellUnavailable` | `shell_unavailable` | 409 / 412 / 500 / 503 | indicates a shell unavailable condition. |
| `StoreUnavailable` | `store_unavailable` | 409 / 412 / 500 / 503 | indicates a store unavailable condition. |
| `StreamTicketsUnavailable` | `stream_tickets_unavailable` | 409 / 412 / 500 / 503 | indicates a stream tickets unavailable condition. |
| `TemplateInUse` | `template_in_use` | 409 / 412 / 500 / 503 | indicates a template in use condition. |
| `TunnelUnavailable` | `tunnel_unavailable` | 409 / 412 / 500 / 503 | indicates a tunnel unavailable condition. |
| `TunnelUnwired` | `tunnel_unwired` | 409 / 412 / 500 / 503 | indicates a tunnel unwired condition. |
| `Unavailable` | `unavailable` | 409 / 412 / 500 / 503 | indicates a unavailable condition. |

### Conflict / state and lifecycle violations

Dominant HTTP status: 409 · Provenance: codemod

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `AlreadyDelivered` | `already_delivered` | 409 | indicates a already delivered condition. |
| `AlreadyExpired` | `already_expired` | 409 | indicates a already expired condition. |
| `NewerApplicationExists` | `newer_application_exists` | 409 | indicates a newer application exists condition. |
| `SessionNotActive` | `session_not_active` | 409 | indicates a session not active condition. |
| `SessionNotOwned` | `session_not_owned` | 409 | indicates a session not owned condition. |
| `SnapshotNotReady` | `snapshot_not_ready` | 409 | indicates a snapshot not ready condition. |
| `TransitionError` | `transition_error` | 409 | indicates a transition error condition. Aliases: `invalid_transition` |

### Server / IO / dependency failures

Dominant HTTP status: 500 · Provenance: codemod

| Constant | Wire value | HTTP | Description |
| --- | --- | --- | --- |
| `ActivityError` | `activity_error` | 500 | indicates a activity error condition. |
| `AgentConnectionError` | `agent_connection_error` | 500 | indicates a agent connection error condition. |
| `AgentFleetError` | `agent_fleet_error` | 500 | indicates a agent fleet error condition. |
| `AggregateError` | `aggregate_error` | 500 | indicates a aggregate error condition. Aliases: `aggregate_failed` |
| `AlertError` | `alert_error` | 500 | indicates a alert error condition. |
| `ApplyError` | `apply_error` | 500 | indicates a apply error condition. |
| `ArgoCDError` | `argocd_error` | 500 | indicates a argo cd error condition. |
| `ArgoCDSecretError` | `argocd_secret_error` | 500 | indicates a argo cd secret error condition. |
| `AsynqError` | `asynq_error` | 500 | indicates a asynq error condition. |
| `AttachError` | `attach_error` | 500 | indicates a attach error condition. |
| `AuditError` | `audit_error` | 500 | indicates a audit error condition. |
| `BuildError` | `build_error` | 500 | indicates a build error condition. |
| `CRCreateError` | `cr_create_error` | 500 | indicates a cr create error condition. |
| `CancelFailed` | `cancel_failed` | 500 | indicates a cancel failed condition. |
| `ClusterLookupFailed` | `cluster_lookup_failed` | 500 | indicates a cluster lookup failed condition. |
| `CountCVEError` | `count_cve_error` | 500 | indicates a count cve error condition. |
| `CreateDecommissionFailed` | `create_decommission_failed` | 500 | indicates a create decommission failed condition. |
| `CryptoError` | `crypto_error` | 500 | indicates a crypto error condition. |
| `DecisionError` | `decision_error` | 500 | indicates a decision error condition. |
| `DecryptError` | `decrypt_error` | 500 | indicates a decrypt error condition. |
| `DetachError` | `detach_error` | 500 | indicates a detach error condition. |
| `DetectFailed` | `detect_failed` | 500 | indicates a detect failed condition. |
| `DiffError` | `diff_error` | 500 | indicates a diff error condition. |
| `EncodeError` | `encode_error` | 500 | indicates a encode error condition. |
| `EncryptError` | `encrypt_error` | 500 | indicates a encrypt error condition. |
| `EncryptionError` | `encryption_error` | 500 | indicates a encryption error condition. |
| `EnqueueError` | `enqueue_error` | 500 | indicates a enqueue error condition. |
| `ExportError` | `export_error` | 500 | indicates a export error condition. |
| `GenerateError` | `generate_error` | 500 | indicates a generate error condition. |
| `GetError` | `get_error` | 500 | indicates a get error condition. |
| `HashError` | `hash_error` | 500 | indicates a hash error condition. |
| `HelmError` | `helm_error` | 500 | indicates a helm error condition. |
| `HistogramFailed` | `histogram_failed` | 500 | indicates a histogram failed condition. |
| `HistoryError` | `history_error` | 500 | indicates a history error condition. |
| `InstallFailed` | `install_failed` | 500 | indicates a install failed condition. |
| `InventoryError` | `inventory_error` | 500 | indicates a inventory error condition. |
| `K8sError` | `k8s_error` | 500 | indicates a k8s error condition. |
| `ListCVEError` | `list_cve_error` | 500 | indicates a list cve error condition. |
| `ListClustersFailed` | `list_clusters_failed` | 500 | indicates a list clusters failed condition. |
| `LoadError` | `load_error` | 500 | indicates a load error condition. |
| `LookupError` | `lookup_error` | 500 | indicates a lookup error condition. Aliases: `lookup_failed` |
| `MTLSError` | `mtls_error` | 500 | indicates a mtls error condition. |
| `MarshalError` | `marshal_error` | 500 | indicates a marshal error condition. |
| `MetricsError` | `metrics_error` | 500 | indicates a metrics error condition. |
| `MonitoringError` | `monitoring_error` | 500 | indicates a monitoring error condition. |
| `OwnershipError` | `ownership_error` | 500 | indicates a ownership error condition. |
| `PersistError` | `persist_failed` | 500 | indicates a persist error condition. |
| `PolicyError` | `policy_error` | 500 | indicates a policy error condition. |
| `PostDetectRead` | `post_detect_read` | 500 | indicates a post detect read condition. |
| `PreviewError` | `preview_error` | 500 | indicates a preview error condition. |
| `ProjectLookupError` | `project_lookup_error` | 500 | indicates a project lookup error condition. |
| `ProxyError` | `proxy_error` | 500 | indicates a proxy error condition. |
| `QuotaCheckError` | `quota_check_error` | 500 | indicates a quota check error condition. |
| `ReadError` | `read_error` | 500 | indicates a read error condition. |
| `ReapplyError` | `reapply_error` | 500 | indicates a reapply error condition. |
| `RegistrationError` | `registration_error` | 500 | indicates a registration error condition. |
| `RenderError` | `render_error` | 500 | indicates a render error condition. |
| `ResolveError` | `resolve_error` | 500 | indicates a resolve error condition. |
| `RestartError` | `restart_error` | 500 | indicates a restart error condition. |
| `RetryError` | `retry_error` | 500 | indicates a retry error condition. Aliases: `retry_failed` |
| `RevertError` | `revert_error` | 500 | indicates a revert error condition. |
| `RevokeError` | `revoke_error` | 500 | indicates a revoke error condition. |
| `RollbackError` | `rollback_error` | 500 | indicates a rollback error condition. |
| `SaveError` | `save_error` | 500 | indicates a save error condition. |
| `SettingsError` | `settings_error` | 500 | indicates a settings error condition. |
| `ShellCloseFailed` | `shell_close_failed` | 500 | indicates a shell close failed condition. |
| `ShellOpenFailed` | `shell_open_failed` | 500 | indicates a shell open failed condition. |
| `SilenceError` | `silence_error` | 500 | indicates a silence error condition. |
| `SnapshotParse` | `snapshot_parse` | 500 | indicates a snapshot parse condition. |
| `StatusError` | `status_error` | 500 | indicates a status error condition. |
| `SubscribeError` | `subscribe_error` | 500 | indicates a subscribe error condition. |
| `SyncError` | `sync_error` | 500 | indicates a sync error condition. |
| `TaskError` | `task_error` | 500 | indicates a task error condition. |
| `TestFailed` | `test_failed` | 500 | indicates a test failed condition. |
| `TicketError` | `ticket_error` | 500 | indicates a ticket error condition. |
| `UsersError` | `users_error` | 500 | indicates a users error condition. |
| `VaultResolveFailed` | `vault_resolve_failed` | 500 | indicates a vault resolve failed condition. |
| `VeleroMissingOnTarget` | `velero_missing_on_target` | 500 | indicates a velero missing on target condition. |
| `VeleroUnreachable` | `velero_unreachable` | 500 | indicates a velero unreachable condition. |
| `WorkloadError` | `workload_error` | 500 | indicates a workload error condition. |
| `WriteError` | `write_error` | 500 | indicates a write error condition. |

## Legacy literal aliases

Near-duplicate wire spellings that were collapsed onto a single canonical code.
Clients that previously observed a legacy literal should branch on the canonical
wire value instead.

| Legacy literal | Canonical wire value | Constant |
| --- | --- | --- |
| `invalid_component` | `component_invalid` | `ComponentInvalid` |
| `since_invalid` | `invalid_since` | `InvalidSince` |
| `invalid_transition` | `transition_error` | `TransitionError` |
| `aggregate_failed` | `aggregate_error` | `AggregateError` |
| `lookup_failed` | `lookup_error` | `LookupError` |
| `retry_failed` | `retry_error` | `RetryError` |
