// Package apiutil is the single error contract of the Astronomer API
// (guide ch.03, ADR-0012).
//
// It owns three things and nothing else:
//
//   - the closed catalog of machine-readable error codes (this file). Code is a
//     defined type, so a handler cannot invent a code at a call site: every
//     value must be a constant declared here, and catalog.go maps each one to
//     exactly one connect.Code and one canonical HTTP status;
//
//   - FromError, which translates domain errors (sentinels, pgx/pgconn errors,
//     context errors, *Error) into that catalog and defaults everything else to
//     Internal with the original error preserved as the log-only cause;
//
//   - the one HTTP writer (WriteError / Respond), which emits
//
//     {"error": {"code": "<code>", "message": "<message>", "request_id": "..."}}
//
//     scrubs every 5xx message, and records
//     astronomer_request_errors_total{route,reason}.
//
// Phase 2 mounts Connect handlers; they translate through the same catalog via
// ConnectError, so REST and RPC report one vocabulary.
package apiutil

// Code is a stable, machine-readable API error identifier. It is a defined
// type (not an alias) so the set of codes a client can observe is closed at
// compile time: only the constants below exist.
type Code string

// --- Validation / bad client input (typically HTTP 400) ---

const (
	// OffsetNotSupported rejects a continuation beyond the bounded legacy range.
	OffsetNotSupported Code = "offset_not_supported"

	// CursorNotSupported rejects an opaque continuation on a legacy collection.
	CursorNotSupported Code = "cursor_not_supported"

	// AgentOverloaded indicates the connected cluster agent reached its bounded
	// in-flight request capacity. Clients may retry after the response's
	// Retry-After interval (HTTP 429).
	AgentOverloaded Code = "agent_overloaded"

	// InvalidBody indicates the request body could not be decoded (malformed
	// JSON or wrong shape).
	InvalidBody Code = "invalid_body"

	// InvalidID indicates a path or query identifier failed to parse (e.g. a
	// non-UUID id).
	InvalidID Code = "invalid_id"

	// ValidationError indicates the request was well-formed but failed
	// field-level validation rules.
	ValidationError Code = "validation_error"

	// InvalidRequest indicates a generically malformed or unsatisfiable
	// request that is not covered by a more specific validation code.
	InvalidRequest Code = "invalid_request"

	// InvalidName indicates a supplied name violates its naming constraints.
	InvalidName Code = "invalid_name"

	// InvalidToken indicates a supplied token is missing or malformed. (When
	// used in an authentication context this typically accompanies a 401.)
	InvalidToken Code = "invalid_token"
)

// --- Not found (HTTP 404) ---

const (
	// NotFound indicates the requested resource does not exist. Prefer this
	// generic code over entity-specific variants (cluster_not_found, etc.).
	NotFound Code = "not_found"
)

// --- Conflict / state and uniqueness violations (HTTP 409) ---

const (
	// Conflict indicates the request conflicts with the current state of the
	// resource (uniqueness violation, illegal state transition, etc.).
	Conflict Code = "conflict"
)

// --- Authentication and authorization (HTTP 401 / 403) ---

const (
	// AuthenticationRequired indicates the caller is not authenticated and a
	// credential is required (HTTP 401).
	AuthenticationRequired Code = "authentication_required"

	// Forbidden indicates the caller is authenticated but lacks permission for
	// the requested operation (HTTP 403).
	Forbidden Code = "forbidden"

	// ScopeDenied indicates the caller's credential (e.g. an API token) is
	// missing a required OAuth-style scope, distinct from an RBAC permission
	// denial. Distinguishes "your token can't do this" from "you can't do
	// this" (HTTP 403). Not collapsed into Forbidden: clients branch on it to
	// prompt for a re-scoped token rather than an access request.
	ScopeDenied Code = "scope_denied"
)

// --- Server / IO / database failures (typically HTTP 500) ---

const (
	// InternalError indicates an unexpected server-side failure with no more
	// specific classification.
	InternalError Code = "internal_error"

	// DBError indicates a database query or transaction failed.
	DBError Code = "db_error"

	// ListError indicates a list/read query backing a collection endpoint
	// failed.
	ListError Code = "list_error"

	// CountError indicates a count query backing a paginated endpoint failed.
	CountError Code = "count_error"

	// CreateError indicates a resource could not be created due to a
	// server-side failure.
	CreateError Code = "create_error"

	// UpdateError indicates a resource could not be updated due to a
	// server-side failure.
	UpdateError Code = "update_error"

	// DeleteError indicates a resource could not be deleted due to a
	// server-side failure.
	DeleteError Code = "delete_error"
)

// ===========================================================================
// Catalog expansion (Track B2 codemod).
//
// The constants below were generated from the recon catalog of every error
// code literal emitted by internal/handler/*.go. Each constant's value is the
// canonical wire string; near-duplicate spellings were collapsed onto a single
// constant (noted inline) so the observable code set is enumerable.
// ===========================================================================

// --- Validation / bad client input (typically HTTP 400 / 422) ---

const (
	// AccountDisabled indicates a account disabled condition.
	AccountDisabled Code = "account_disabled"

	// AuditRetentionDowngrade indicates a audit retention downgrade condition.
	AuditRetentionDowngrade Code = "audit_retention_downgrade"

	// BaselineDisabled indicates a baseline disabled condition.
	BaselineDisabled Code = "baseline_disabled"

	// BaselineRequired indicates a baseline required condition.
	BaselineRequired Code = "baseline_required"

	// BuiltinReadonly indicates a builtin readonly condition.
	BuiltinReadonly Code = "builtin_readonly"

	// BuiltinTemplate indicates a builtin template condition.
	BuiltinTemplate Code = "builtin_template"

	// ComponentInvalid indicates a component invalid condition.
	// Collapses legacy literal(s): "invalid_component".
	ComponentInvalid Code = "component_invalid"

	// ImmutableName indicates a immutable name condition.
	ImmutableName Code = "immutable_name"

	// ImmutableProvider indicates a immutable provider condition.
	ImmutableProvider Code = "immutable_provider"

	// IncompatibleExtension indicates a incompatible extension condition.
	IncompatibleExtension Code = "incompatible_extension"

	// ExtensionRBACDenied indicates the requesting user's own RBAC bindings do
	// not grant the data source the extension declared (§DataProxy step 4). The
	// extension can never exceed the user — this is the load-bearing deny.
	ExtensionRBACDenied Code = "extension_rbac_denied"

	// InvalidAddr indicates a invalid addr condition.
	InvalidAddr Code = "invalid_addr"

	// InvalidCIDR indicates a invalid cidr condition.
	InvalidCIDR Code = "invalid_cidr"

	// InvalidChallenge indicates a invalid challenge condition.
	InvalidChallenge Code = "invalid_challenge"

	// InvalidClusterID indicates an invalid cluster condition. The wire value is
	// "invalid_cluster"; the Go identifier keeps the ID suffix for catalog
	// consistency with the other invalid-ID codes.
	InvalidClusterID Code = "invalid_cluster"

	// InvalidCode indicates a invalid code condition.
	InvalidCode Code = "invalid_code"

	// InvalidContext indicates a invalid context condition.
	InvalidContext Code = "invalid_context"

	// InvalidEmail indicates a invalid email condition.
	InvalidEmail Code = "invalid_email"

	// InvalidField indicates a invalid field condition.
	InvalidField Code = "invalid_field"

	// InvalidFilter indicates a invalid filter condition.
	InvalidFilter Code = "invalid_filter"

	// InvalidFormat indicates a invalid format condition.
	InvalidFormat Code = "invalid_format"

	// InvalidGroupName indicates a invalid group name condition.
	InvalidGroupName Code = "invalid_group_name"

	// InvalidKey indicates a invalid key condition.
	InvalidKey Code = "invalid_key"

	// InvalidKind indicates a invalid kind condition.
	InvalidKind Code = "invalid_kind"

	// InvalidManifest indicates a invalid manifest condition.
	InvalidManifest Code = "invalid_manifest"

	// InvalidMode indicates a invalid mode condition.
	InvalidMode Code = "invalid_mode"

	// InvalidParent indicates a invalid parent condition.
	InvalidParent Code = "invalid_parent"

	// InvalidPolicy indicates a invalid policy condition.
	InvalidPolicy Code = "invalid_policy"

	// InvalidProvider indicates a invalid provider condition.
	InvalidProvider Code = "invalid_provider"

	// InvalidRange indicates a invalid range condition.
	InvalidRange Code = "invalid_range"

	// InvalidResource indicates a invalid resource condition.
	InvalidResource Code = "invalid_resource"

	// InvalidScope indicates a invalid scope condition.
	InvalidScope Code = "invalid_scope"

	// InvalidSignature indicates a supplied cryptographic signature failed
	// verification against the trusted key.
	InvalidSignature Code = "invalid_signature"

	// InvalidScopeParams indicates a invalid scope params condition.
	InvalidScopeParams Code = "invalid_scope_params"

	// InvalidServiceProxyTarget indicates a invalid service proxy target condition.
	InvalidServiceProxyTarget Code = "invalid_service_proxy_target"

	// InvalidSince indicates a invalid since condition.
	// Collapses legacy literal(s): "since_invalid".
	InvalidSince Code = "invalid_since"

	// InvalidStars indicates a invalid stars condition.
	InvalidStars Code = "invalid_stars"

	// InvalidState indicates a invalid state condition.
	InvalidState Code = "invalid_state"

	// InvalidStatus indicates a invalid status condition.
	InvalidStatus Code = "invalid_status"

	// InvalidStep indicates a invalid step condition.
	InvalidStep Code = "invalid_step"

	// InvalidTaint indicates a invalid taint condition.
	InvalidTaint Code = "invalid_taint"

	// InvalidTargetRefs indicates a invalid target refs condition.
	InvalidTargetRefs Code = "invalid_target_refs"

	// InvalidType indicates a invalid type condition.
	InvalidType Code = "invalid_type"

	// InvalidURL indicates a invalid url condition.
	InvalidURL Code = "invalid_url"

	// MaxDepth indicates a max depth condition.
	MaxDepth Code = "max_depth"

	// MissingCluster indicates a missing cluster condition.
	MissingCluster Code = "missing_cluster"

	// MissingIssuer indicates a missing issuer condition.
	MissingIssuer Code = "missing_issuer"

	// MissingParams indicates a missing params condition.
	MissingParams Code = "missing_params"

	// ModeChangeRequiresForce indicates a mode change requires force condition.
	ModeChangeRequiresForce Code = "mode_change_requires_force"

	// NoDefault indicates a no default condition.
	NoDefault Code = "no_default"

	// NoDestination indicates a no destination condition.
	NoDestination Code = "no_destination"

	// NoSettings indicates a no settings condition.
	NoSettings Code = "no_settings"

	// NoSnapshot indicates a no snapshot condition.
	NoSnapshot Code = "no_snapshot"

	// NoteTooLong indicates a note too long condition.
	NoteTooLong Code = "note_too_long"

	// QueueRequired indicates a queue required condition.
	QueueRequired Code = "queue_required"

	// SMTPDisabled indicates a smtp disabled condition.
	SMTPDisabled Code = "smtp_disabled"

	// StaleDefault indicates a stale default condition.
	StaleDefault Code = "stale_default"

	// StepMismatch indicates a step mismatch condition.
	StepMismatch Code = "step_mismatch"

	// TemplateDisabled indicates a template disabled condition.
	TemplateDisabled Code = "template_disabled"

	// UnknownKey indicates a unknown key condition.
	UnknownKey Code = "unknown_key"

	// UnsupportedProvider indicates a unsupported provider condition.
	UnsupportedProvider Code = "unsupported_provider"

	// UnsupportedType indicates a unsupported type condition.
	UnsupportedType Code = "unsupported_type"
)

// --- Authentication, SSO, MFA and token errors (typically HTTP 401) ---

const (
	// RecoveryFailed indicates a recovery failed condition.
	RecoveryFailed Code = "recovery_failed"

	// SSOCallbackError indicates a sso callback error condition.
	SSOCallbackError Code = "sso_callback_error"

	// SSOError indicates a sso error condition.
	SSOError Code = "sso_error"

	// SSOInvalidRequest indicates a sso invalid request condition.
	SSOInvalidRequest Code = "sso_invalid_request"

	// SSOInvalidState indicates a sso invalid state condition.
	SSOInvalidState Code = "sso_invalid_state"

	// SSOMissingEmail indicates a sso missing email condition.
	SSOMissingEmail Code = "sso_missing_email"

	// SSONotConfigured indicates a sso not configured condition.
	SSONotConfigured Code = "sso_not_configured"

	// SSOProviderError indicates a sso provider error condition.
	SSOProviderError Code = "sso_provider_error"

	// SSOUserError indicates a sso user error condition.
	SSOUserError Code = "sso_user_error"

	// TOTPEncryptFailed indicates a totp encrypt failed condition.
	TOTPEncryptFailed Code = "totp_encrypt_failed"

	// TOTPGenerateFailed indicates a totp generate failed condition.
	TOTPGenerateFailed Code = "totp_generate_failed"

	// TokenError indicates a token error condition.
	TokenError Code = "token_error"

	// TokenGenerationError indicates a token generation error condition.
	TokenGenerationError Code = "token_generation_error"
)

// --- Authorization / access control (typically HTTP 403) ---

const (
	// AccountLocked indicates a account locked condition.
	AccountLocked Code = "account_locked"

	// CSRFRequired indicates a csrf required condition.
	CSRFRequired Code = "csrf_required"

	// ServiceProxyDenied indicates a service proxy denied condition.
	ServiceProxyDenied Code = "service_proxy_denied"

	// WrongClusterScope indicates a wrong cluster scope condition.
	WrongClusterScope Code = "wrong_cluster_scope"
)

// --- Dependency unavailable, not configured, or not wired (typically HTTP 409 / 412 / 500 / 503) ---

const (
	// ClusterAgentUnavailable indicates a cluster agent unavailable condition.
	ClusterAgentUnavailable Code = "cluster_agents_unavailable"

	// CatalogUnavailable indicates a catalog unavailable condition.
	CatalogUnavailable Code = "catalog_unavailable"

	// DBUnavailable indicates a db unavailable condition.
	DBUnavailable Code = "db_unavailable"

	// DetectorUnwired indicates a detector unwired condition.
	DetectorUnwired Code = "detector_unwired"

	// DiffUnavailable indicates a diff unavailable condition.
	DiffUnavailable Code = "diff_unavailable"

	// EncryptUnavailable indicates a encrypt unavailable condition.
	EncryptUnavailable Code = "encrypt_unavailable"

	// HistoryUnavailable indicates a history unavailable condition.
	HistoryUnavailable Code = "history_unavailable"

	// InspectorUnavailable indicates a inspector unavailable condition.
	InspectorUnavailable Code = "inspector_unavailable"

	// LogsUnavailable indicates a logs unavailable condition.
	LogsUnavailable Code = "logs_unavailable"

	// NotCancellable indicates a not cancellable condition.
	NotCancellable Code = "not_cancellable"

	// NotConfigured indicates a not configured condition.
	NotConfigured Code = "not_configured"

	// NotEnrolled indicates a not enrolled condition.
	NotEnrolled Code = "not_enrolled"

	// NotFailed indicates a not failed condition.
	NotFailed Code = "not_failed"

	// NotImplemented indicates a not implemented condition.
	NotImplemented Code = "not_implemented"

	// NotWired indicates a not wired condition.
	NotWired Code = "not_wired"

	// PlanInUse indicates a plan in use condition.
	PlanInUse Code = "plan_in_use"

	// PlanIsReserved indicates a plan is reserved condition.
	PlanIsReserved Code = "plan_is_reserved"

	// RBACUnavailable indicates a rbac unavailable condition.
	RBACUnavailable Code = "rbac_unavailable"

	// RunnerUnwired indicates a runner unwired condition.
	RunnerUnwired Code = "runner_unwired"

	// SSOUnavailable indicates a sso unavailable condition.
	SSOUnavailable Code = "sso_unavailable"

	// SearchUnavailable indicates a search unavailable condition.
	SearchUnavailable Code = "search_unavailable"

	// ShellUnavailable indicates a shell unavailable condition.
	ShellUnavailable Code = "shell_unavailable"

	// SizerFailed indicates a management-cluster sizer precheck refused
	// the install or replace (HTTP 412). Grafana uses leftover floor;
	// Loki uses the fail-closed mode gate.
	SizerFailed Code = "sizer_failed"

	// StoreUnavailable indicates a store unavailable condition.
	StoreUnavailable Code = "store_unavailable"

	// StreamTicketsUnavailable indicates a stream tickets unavailable condition.
	StreamTicketsUnavailable Code = "stream_tickets_unavailable"

	// TemplateInUse indicates a template in use condition.
	TemplateInUse Code = "template_in_use"

	// TunnelUnavailable indicates a tunnel unavailable condition.
	TunnelUnavailable Code = "tunnel_unavailable"

	// TunnelUnwired indicates a tunnel unwired condition.
	TunnelUnwired Code = "tunnel_unwired"

	// Unavailable indicates a unavailable condition.
	Unavailable Code = "unavailable"
)

// --- Conflict / state and lifecycle violations (typically HTTP 409) ---

const (
	// AlreadyDelivered indicates a already delivered condition.
	AlreadyDelivered Code = "already_delivered"

	// AlreadyExpired indicates a already expired condition.
	AlreadyExpired Code = "already_expired"

	// NewerApplicationExists indicates a newer application exists condition.
	NewerApplicationExists Code = "newer_application_exists"

	// SessionNotActive indicates a session not active condition.
	SessionNotActive Code = "session_not_active"

	// SessionNotOwned indicates a session not owned condition.
	SessionNotOwned Code = "session_not_owned"

	// SnapshotNotReady indicates a snapshot not ready condition.
	SnapshotNotReady Code = "snapshot_not_ready"

	// TransitionError indicates a transition error condition.
	// Collapses legacy literal(s): "invalid_transition".
	TransitionError Code = "transition_error"

	// DegradedCapacity indicates hosted Loki is frozen for new attaches
	// because a sizer re-eval set status degraded_capacity (HTTP 409).
	DegradedCapacity Code = "degraded_capacity"

	// IngestCapExceeded indicates attaching another cluster would fail the
	// running Loki mode's pass row or observed ingest is ≥ 80% of the
	// global budget (HTTP 409).
	IngestCapExceeded Code = "ingest_cap_exceeded"

	// LokiNotReady indicates Astronomer Loki is not healthy with a public
	// ingest hostname, so one-click attach cannot proceed (HTTP 409).
	LokiNotReady Code = "loki_not_ready"
)

// --- Server / IO / dependency failures (typically HTTP 500) ---

const (
	// ActivityError indicates a activity error condition.
	ActivityError Code = "activity_error"

	// AgentConnectionError indicates a agent connection error condition.
	AgentConnectionError Code = "agent_connection_error"

	// ClusterAgentError indicates a cluster agent error condition.
	ClusterAgentError Code = "cluster_agents_error"

	// AggregateError indicates a aggregate error condition.
	// Collapses legacy literal(s): "aggregate_failed".
	AggregateError Code = "aggregate_error"

	// AlertError indicates a alert error condition.
	AlertError Code = "alert_error"

	// ApplyError indicates a apply error condition.
	ApplyError Code = "apply_error"

	// AsynqError indicates a asynq error condition.
	AsynqError Code = "asynq_error"

	// AuditError indicates a audit error condition.
	AuditError Code = "audit_error"

	// CRCreateError indicates a cr create error condition.
	CRCreateError Code = "cr_create_error"

	// CancelFailed indicates a cancel failed condition.
	CancelFailed Code = "cancel_failed"

	// ClusterLookupFailed indicates a cluster lookup failed condition.
	ClusterLookupFailed Code = "cluster_lookup_failed"

	// CountCVEError indicates a count cve error condition.
	CountCVEError Code = "count_cve_error"

	// CreateDecommissionFailed indicates a create decommission failed condition.
	CreateDecommissionFailed Code = "create_decommission_failed"

	// CryptoError indicates a crypto error condition.
	CryptoError Code = "crypto_error"

	// DecryptError indicates a decrypt error condition.
	DecryptError Code = "decrypt_error"

	// DetachError indicates a detach error condition.
	DetachError Code = "detach_error"

	// DetectFailed indicates a detect failed condition.
	DetectFailed Code = "detect_failed"

	// DiffError indicates a diff error condition.
	DiffError Code = "diff_error"

	// EncodeError indicates a encode error condition.
	EncodeError Code = "encode_error"

	// EncryptError indicates a encrypt error condition.
	EncryptError Code = "encrypt_error"

	// EnqueueError indicates a enqueue error condition.
	EnqueueError Code = "enqueue_error"

	// ExportError indicates a export error condition.
	ExportError Code = "export_error"

	// GenerateError indicates a generate error condition.
	GenerateError Code = "generate_error"

	// GetError indicates a get error condition.
	GetError Code = "get_error"

	// HashError indicates a hash error condition.
	HashError Code = "hash_error"

	// HelmError indicates a helm error condition.
	HelmError Code = "helm_error"

	// HistogramFailed indicates a histogram failed condition.
	HistogramFailed Code = "histogram_failed"

	// HistoryError indicates a history error condition.
	HistoryError Code = "history_error"

	// InstallFailed indicates a install failed condition.
	InstallFailed Code = "install_failed"

	// InventoryError indicates a inventory error condition.
	InventoryError Code = "inventory_error"

	// K8sError indicates a k8s error condition.
	K8sError Code = "k8s_error"

	// ListCVEError indicates a list cve error condition.
	ListCVEError Code = "list_cve_error"

	// ListClustersFailed indicates a list clusters failed condition.
	ListClustersFailed Code = "list_clusters_failed"

	// LoadError indicates a load error condition.
	LoadError Code = "load_error"

	// LookupError indicates a lookup error condition.
	// Collapses legacy literal(s): "lookup_failed".
	LookupError Code = "lookup_error"

	// MTLSError indicates a mtls error condition.
	MTLSError Code = "mtls_error"

	// MarshalError indicates a marshal error condition.
	MarshalError Code = "marshal_error"

	// MetricsError indicates a metrics error condition.
	MetricsError Code = "metrics_error"

	// MonitoringError indicates a monitoring error condition.
	MonitoringError Code = "monitoring_error"

	// PolicyError indicates a policy error condition.
	PolicyError Code = "policy_error"

	// PostDetectRead indicates a post detect read condition.
	PostDetectRead Code = "post_detect_read"

	// PreviewError indicates a preview error condition.
	PreviewError Code = "preview_error"

	// ProxyError indicates a proxy error condition.
	ProxyError Code = "proxy_error"

	// QuotaCheckError indicates a quota check error condition.
	QuotaCheckError Code = "quota_check_error"

	// ReadError indicates a read error condition.
	ReadError Code = "read_error"

	// ReapplyError indicates a reapply error condition.
	ReapplyError Code = "reapply_error"

	// RegistrationError indicates a registration error condition.
	RegistrationError Code = "registration_error"

	// RenderError indicates a render error condition.
	RenderError Code = "render_error"

	// ResolveError indicates a resolve error condition.
	ResolveError Code = "resolve_error"

	// RetryError indicates a retry error condition.
	// Collapses legacy literal(s): "retry_failed".
	RetryError Code = "retry_error"

	// RevertError indicates a revert error condition.
	RevertError Code = "revert_error"

	// RevokeError indicates a revoke error condition.
	RevokeError Code = "revoke_error"

	// SaveError indicates a save error condition.
	SaveError Code = "save_error"

	// SettingsError indicates a settings error condition.
	SettingsError Code = "settings_error"

	// ShellCloseFailed indicates a shell close failed condition.
	ShellCloseFailed Code = "shell_close_failed"

	// ShellOpenFailed indicates a shell open failed condition.
	ShellOpenFailed Code = "shell_open_failed"

	// SilenceError indicates a silence error condition.
	SilenceError Code = "silence_error"

	// SnapshotParse indicates a snapshot parse condition.
	SnapshotParse Code = "snapshot_parse"

	// StatusError indicates a status error condition.
	StatusError Code = "status_error"

	// AuditUnavailable means mandatory compliance evidence could not be
	// persisted, so the sensitive operation was rejected before side effects.
	AuditUnavailable Code = "audit_unavailable"

	// SubscribeError indicates a subscribe error condition.
	SubscribeError Code = "subscribe_error"

	// SyncError indicates a sync error condition.
	SyncError Code = "sync_error"

	// TaskError indicates a task error condition.
	TaskError Code = "task_error"

	// TestFailed indicates a test failed condition.
	TestFailed Code = "test_failed"

	// TicketError indicates a ticket error condition.
	TicketError Code = "ticket_error"

	// UsersError indicates a users error condition.
	UsersError Code = "users_error"

	// VaultResolveFailed indicates a vault resolve failed condition.
	VaultResolveFailed Code = "vault_resolve_failed"

	// VeleroMissingOnTarget indicates a velero missing on target condition.
	VeleroMissingOnTarget Code = "velero_missing_on_target"

	// VeleroUnreachable indicates a velero unreachable condition.
	VeleroUnreachable Code = "velero_unreachable"

	// WorkloadError indicates a workload error condition.
	WorkloadError Code = "workload_error"

	// WriteFailure indicates a write error condition.
	WriteFailure Code = "write_error"
)

// ===========================================================================
// Codes folded in from the former second writers (ADR-0012): the delivery
// package's respondError, the route/middleware inline JSON writers, the
// delivery rollout engine's typed error codes, and the codes FromError
// produces for domain and database errors.
// ===========================================================================

// --- Validation / bad client input ---

const (
	// InvalidProjectScope indicates the project scope of a delivery request
	// could not be resolved from the route.
	InvalidProjectScope Code = "invalid_project_scope"

	// InvalidResourceID indicates a delivery resource id failed to parse.
	InvalidResourceID Code = "invalid_resource_id"

	// InvalidPagination indicates malformed limit/cursor parameters.
	InvalidPagination Code = "invalid_pagination"

	// InvalidIdempotencyKey indicates a malformed Idempotency-Key header.
	InvalidIdempotencyKey Code = "invalid_idempotency_key"

	// InvalidK8sPath indicates a Kubernetes proxy path that is not canonical.
	InvalidK8sPath Code = "invalid_k8s_path"

	// InvalidNamespaceSelection indicates a malformed multi-namespace selection.
	InvalidNamespaceSelection Code = "invalid_namespace_selection"

	// InvalidReference indicates a write referenced a row that does not exist
	// (foreign-key violation on insert/update).
	InvalidReference Code = "invalid_reference"

	// SecretNotAllowed indicates inline secret material where only Kubernetes
	// Secret references are accepted.
	SecretNotAllowed Code = "secret_not_allowed"

	// EstateLimitExceeded indicates a delivery estate exceeds its bounded size.
	EstateLimitExceeded Code = "estate_limit_exceeded"

	// RequestTooLarge indicates a request body exceeded a route's limit.
	RequestTooLarge Code = "request_too_large"

	// RequestBodyTooLarge indicates a request body exceeded the global limit.
	RequestBodyTooLarge Code = "request_body_too_large"

	// RolloutInvalidInput indicates a rollout request failed validation.
	RolloutInvalidInput Code = "invalid_input"

	// RolloutNoClusters indicates a rollout resolved to no target clusters.
	RolloutNoClusters Code = "no_clusters"

	// RolloutInvalidCohorts indicates a rollout cohort plan is invalid.
	RolloutInvalidCohorts Code = "invalid_cohorts"
)

// --- Authentication / authorization ---

const (
	// PermissionDenied indicates the RBAC authorizer refused the request.
	PermissionDenied Code = "permission_denied"

	// IPNotAllowlisted indicates an API token used outside its IP allowlist.
	IPNotAllowlisted Code = "ip_not_allowlisted"

	// PlatformScopeForbidden indicates a project caller addressed a
	// platform-scoped delivery resource.
	PlatformScopeForbidden Code = "platform_scope_forbidden"
)

// --- Not found ---

const (
	// FeatureDisabled indicates the route belongs to a feature switched off in
	// platform settings (reported as 404).
	FeatureDisabled Code = "feature_disabled"
)

// --- Conflict / precondition / concurrency ---

const (
	// AlreadyExists indicates a uniqueness violation: a resource with the same
	// identity already exists.
	AlreadyExists Code = "already_exists"

	// ResourceInUse indicates a delete or update was refused because other
	// rows still reference the resource.
	ResourceInUse Code = "resource_in_use"

	// FailedPrecondition indicates the resource is not in a state that admits
	// the operation.
	FailedPrecondition Code = "failed_precondition"

	// ConcurrentModification indicates the operation lost a race with a
	// concurrent writer (serialization failure / deadlock); retrying is safe.
	ConcurrentModification Code = "concurrent_modification"

	// IdempotencyConflict indicates an Idempotency-Key was reused for a
	// different request.
	IdempotencyConflict Code = "idempotency_conflict"

	// StaleGeneration indicates a write against an outdated generation.
	StaleGeneration Code = "stale_generation"

	// StaleResourceVersion indicates an If-Match/ETag precondition failed.
	StaleResourceVersion Code = "stale_resource_version"

	// PreviewCursorStale indicates a preview cursor no longer matches state.
	PreviewCursorStale Code = "preview_cursor_stale"

	// IfMatchRequired indicates a mutating request lacked If-Match.
	IfMatchRequired Code = "if_match_required"

	// PreconditionRequired indicates a mutating request lacked a required
	// generation precondition.
	PreconditionRequired Code = "precondition_required"

	// PreconditionFailed indicates a request precondition did not hold.
	PreconditionFailed Code = "precondition_failed"

	// InvalidTransition indicates an illegal lifecycle state transition.
	InvalidTransition Code = "invalid_transition"

	// SourceRevoked indicates a delivery source was revoked.
	SourceRevoked Code = "source_revoked"

	// BundleNotReady indicates a delivery bundle is not yet resolvable.
	BundleNotReady Code = "bundle_not_ready"

	// OverrideSetUnavailable indicates an override set is missing, disabled,
	// or outside the project.
	OverrideSetUnavailable Code = "override_set_unavailable"

	// OverrideConflict indicates override patches conflict with each other.
	OverrideConflict Code = "override_conflict"

	// ConfigurationReferenceUnavailable indicates a referenced configuration
	// template or override set cannot be used.
	ConfigurationReferenceUnavailable Code = "configuration_reference_unavailable"

	// RolloutPreviewStale indicates a rollout preview no longer matches state.
	RolloutPreviewStale Code = "preview_stale"

	// RolloutTargetChanged indicates the rollout target changed mid-flight.
	RolloutTargetChanged Code = "target_changed"

	// RolloutLeaseLost indicates the rollout lease moved to another worker.
	RolloutLeaseLost Code = "lease_lost"

	// RolloutStaleFence indicates a fenced write lost to a newer fence token.
	RolloutStaleFence Code = "stale_fence"

	// RolloutInvariant indicates a rollout invariant was violated.
	RolloutInvariant Code = "invariant_violation"
)

// --- Rate limiting, cancellation, deadlines ---

const (
	// RateLimited indicates the caller exceeded a request budget.
	RateLimited Code = "rate_limited"

	// CharlieRateLimited indicates Charlie request capacity is exhausted.
	CharlieRateLimited Code = "charlie_rate_limited"

	// RequestCanceled indicates the operation was canceled before completion.
	RequestCanceled Code = "request_canceled"

	// DeadlineExceeded indicates the operation ran past its deadline.
	DeadlineExceeded Code = "deadline_exceeded"
)

// --- Server / dependency failures ---

const (
	// ServiceUnavailable indicates a required backing service is unavailable.
	ServiceUnavailable Code = "service_unavailable"

	// SecurityDependencyUnavailable indicates authentication or authorization
	// wiring is missing, so the route fails closed.
	SecurityDependencyUnavailable Code = "security_dependency_unavailable"

	// CredentialEncryptionUnavailable indicates the credential encryptor is
	// not wired.
	CredentialEncryptionUnavailable Code = "credential_encryption_unavailable"

	// InvalidPersistedState indicates stored state failed to decode.
	InvalidPersistedState Code = "invalid_persisted_state"

	// ResolutionEnqueueFailed indicates a source resolution job could not be
	// enqueued.
	ResolutionEnqueueFailed Code = "resolution_enqueue_failed"
)
