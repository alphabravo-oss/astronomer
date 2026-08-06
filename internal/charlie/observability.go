package charlie

import (
	"context"
	"log/slog"
	"strings"
)

// DecisionLog is a deliberately content-free authority event. Correlation IDs
// are opaque and bounded; resource names, prompts, evidence, arguments,
// authorization references, URLs, errors, and credentials have no fields here.
type DecisionLog struct {
	SessionID  string
	ActionID   string
	Capability string
	Mode       Mode
	Effect     Effect
	Decision   AuthorityDecision
}

type operationalLogKind uint8

const (
	operationalLogAuthority operationalLogKind = iota + 1
	operationalLogHTTP
	operationalLogFailure
	operationalLogRuntime
)

type operationalLogRecord struct {
	kind          operationalLogKind
	decision      DecisionLog
	failureCode   string
	correlationID string
	requestID     string
	method        string
	statusCode    int
	durationMS    int64
	runtimeState  string
}

var operationalFailureCodes = map[string]struct{}{
	"charlie.lifecycle_audit_persist_failed":   {},
	"charlie.lifecycle_audit_encode_failed":    {},
	"charlie.finding_local_commit_failed":      {},
	"charlie.action_audit_persist_failed":      {},
	"charlie.finding_persist_failed":           {},
	"charlie.action_receipt_persist_failed":    {},
	"charlie.admin_audit_persist_failed":       {},
	"charlie.http_audit_encode_failed":         {},
	"charlie.http_audit_persist_failed":        {},
	"mode.request_invalid":                     {},
	"mode.integration_inactive":                {},
	"mode.local_revision_conflict":             {},
	"mode.auto_prerequisites_incomplete":       {},
	"mode.audit_persist_failed":                {},
	"mode.local_request_persist_failed":        {},
	"mode.remote_readback_unavailable":         {},
	"mode.remote_installation_changed":         {},
	"mode.remote_mode_mismatch":                {},
	"mode.remote_revision_stale":               {},
	"mode.remote_disclosure_missing":           {},
	"mode.local_verification_persist_failed":   {},
	"mode.reconcile_failed":                    {},
	"onboarding.dependencies_unavailable":      {},
	"onboarding.audit_unavailable":             {},
	"onboarding.package_lookup_failed":         {},
	"onboarding.installation_identity_failed":  {},
	"onboarding.local_trust_failed":            {},
	"onboarding.namespace_prepare_failed":      {},
	"onboarding.credential_expiry_invalid":     {},
	"onboarding.certificate_expiry_invalid":    {},
	"onboarding.connection_create_failed":      {},
	"onboarding.automation_identity_failed":    {},
	"onboarding.trigger_defaults_failed":       {},
	"onboarding.secret_materialization_failed": {},
	"onboarding.repository_prune_failed":       {},
	"onboarding.agent_install_failed":          {},
	"onboarding.state_record_failed":           {},
	"onboarding.state_consume_failed":          {},
	"onboarding.rollback_failed":               {},
	"onboarding.unknown":                       {},
	"bootstrap.secret_writer_unavailable":      {},
	"bootstrap.agent_installer_unavailable":    {},
	"bootstrap.admin_lifecycle_unavailable":    {},
	"bootstrap.admin_service_unavailable":      {},
	"runtime.activation_failed":                {},
	"runtime.feature_dependencies_incomplete":  {},
	"runtime.shutdown_failed":                  {},
}

// LogAuthorityDecision remains the compatibility entry point for focused
// callers. Production paths should use LogAuthorityDecisionContext so request
// cancellation and trace context remain attached to the record.
func LogAuthorityDecision(logger *slog.Logger, event DecisionLog) {
	LogAuthorityDecisionContext(context.Background(), logger, event)
}

func LogAuthorityDecisionContext(ctx context.Context, logger *slog.Logger, event DecisionLog) {
	logCharlieOperational(ctx, logger, operationalLogRecord{kind: operationalLogAuthority, decision: event})
}

// LogOperationalFailure accepts only the closed failure-code vocabulary
// above. An unknown value is represented by a fixed fallback rather than
// copying caller, provider, database, or model text into the log.
func LogOperationalFailure(ctx context.Context, logger *slog.Logger, failureCode, correlationID string) {
	if _, ok := operationalFailureCodes[failureCode]; !ok {
		failureCode = "charlie.unclassified_failure"
	}
	logCharlieOperational(ctx, logger, operationalLogRecord{kind: operationalLogFailure, failureCode: failureCode, correlationID: correlationID})
}

// LogHTTPAudit serializes only bounded protocol metadata. Request and
// correlation identifiers are one-way digests; paths, query values, headers,
// bodies, resource identifiers, IPs, and response text have no representation.
func LogHTTPAudit(ctx context.Context, logger *slog.Logger, method string, statusCode int, durationMS int64, requestID, correlationID string) {
	logCharlieOperational(ctx, logger, operationalLogRecord{
		kind: operationalLogHTTP, method: method, statusCode: statusCode, durationMS: durationMS,
		requestID: requestID, correlationID: correlationID,
	})
}

func LogRuntimeEvent(ctx context.Context, logger *slog.Logger, state string) {
	switch state {
	case "inactive_reconciliation_failed", "reconciliation_pending", "activated", "listener_stopped":
	default:
		state = "unclassified"
	}
	logCharlieOperational(ctx, logger, operationalLogRecord{kind: operationalLogRuntime, runtimeState: state})
}

// logCharlieOperational is the only slog serialization sink for Charlie
// operational records. Every key and non-digest value comes from a closed
// vocabulary selected here.
func logCharlieOperational(ctx context.Context, logger *slog.Logger, record operationalLogRecord) {
	if logger == nil {
		logger = slog.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	switch record.kind {
	case operationalLogAuthority:
		event := record.decision
		result, code := "denied", string(event.Decision.Code)
		if event.Decision.Allowed {
			result, code = "allowed", "allowed"
		} else if !isAuthorityDenialCode(event.Decision.Code) {
			code = string(DeniedAuthorization)
		}
		capability := "unknown"
		if descriptor, found := capabilityByName(event.Capability); found {
			capability = descriptor.Name
		}
		mode := event.Mode
		if !validMode(mode) {
			mode = ModeDisabled
		}
		effect := string(event.Effect)
		if event.Effect != EffectRead && event.Effect != EffectWrite {
			effect = "unknown"
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "Charlie operational event",
			slog.String("event", "charlie_authority_decision"),
			slog.String("session_digest", digestBytes([]byte(event.SessionID))),
			slog.String("action_digest", digestBytes([]byte(event.ActionID))),
			slog.String("capability", capability), slog.String("mode", string(mode)),
			slog.String("effect", effect), slog.String("result", result), slog.String("code", code))
	case operationalLogHTTP:
		method := strings.ToUpper(strings.TrimSpace(record.method))
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			method = "UNKNOWN"
		}
		status := record.statusCode
		if status < 100 || status > 599 {
			status = 500
		}
		duration := record.durationMS
		if duration < 0 {
			duration = 0
		} else if duration > 86_400_000 {
			duration = 86_400_000
		}
		outcome := "failed"
		switch {
		case status >= 200 && status < 400:
			outcome = "success"
		case status == 401 || status == 403:
			outcome = "denied"
		case status >= 400 && status < 500:
			outcome = "rejected"
		}
		logger.LogAttrs(ctx, slog.LevelInfo, "Charlie operational event",
			slog.String("event", "charlie.http.mutation"), slog.String("outcome_code", outcome),
			slog.String("method", method), slog.Int("status_code", status), slog.Int64("duration_ms", duration),
			slog.String("request_digest", digestBytes([]byte(record.requestID))),
			slog.String("correlation_digest", digestBytes([]byte(record.correlationID))))
	case operationalLogFailure:
		logger.LogAttrs(ctx, slog.LevelWarn, "Charlie operational event",
			slog.String("event", "charlie.operational.failure"), slog.String("failure_code", record.failureCode),
			slog.String("correlation_digest", digestBytes([]byte(record.correlationID))))
	case operationalLogRuntime:
		level := slog.LevelWarn
		switch record.runtimeState {
		case "activated":
			level = slog.LevelInfo
		case "inactive_reconciliation_failed":
			level = slog.LevelDebug
		}
		logger.LogAttrs(ctx, level, "Charlie operational event",
			slog.String("event", "charlie.runtime.lifecycle"), slog.String("state", record.runtimeState))
	}
}
