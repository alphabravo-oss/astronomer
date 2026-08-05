package charlie

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/prometheus/client_golang/prometheus"
)

// Charlie metrics intentionally expose only fixed-vocabulary operational
// metadata. Product resource IDs, user/session/action IDs, prompts, evidence,
// arguments, URLs, authorization references, and error strings are never
// labels.
var (
	charlieBridgeCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_bridge_calls_total",
		Help: "Product Bridge calls by bounded operation and outcome.",
	}, []string{"operation", "outcome"})
	charlieBridgeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_charlie_bridge_call_duration_seconds",
		Help:    "Product Bridge call latency by bounded operation and outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation", "outcome"})
	charlieActions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_actions_total",
		Help: "Product-owned MCP action decisions by disclosed capability, effect, state, and bounded policy code.",
	}, []string{"capability", "effect", "state", "code"})
	charlieTriggers = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_trigger_events_total",
		Help: "Durable Charlie trigger lifecycle decisions by reviewed rule and outcome.",
	}, []string{"rule", "outcome"})
	charlieActivation = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_charlie_activation_state",
		Help: "One-hot current Charlie product integration activation state.",
	}, []string{"state"})
	charlieEffectiveMode = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_charlie_effective_mode",
		Help: "One-hot least-authority Charlie mode after product and central intersection.",
	}, []string{"mode"})
	charlieModeDrift = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_charlie_mode_drift",
		Help: "Whether requested and Charlie-verified modes disagree; effective authority remains the lesser mode.",
	})
	charlieSSEConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_charlie_sse_connections",
		Help: "Current authenticated browser-to-Product-Bridge Charlie event streams.",
	})
	charlieSSEEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_sse_events_total",
		Help: "Charlie stream lifecycle and acknowledgement events using fixed vocabulary.",
	}, []string{"event"})
	charlieMCPCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_mcp_calls_total",
		Help: "Private MCP requests by fixed protocol method and bounded outcome.",
	}, []string{"method", "outcome"})
	charlieMCPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_charlie_mcp_call_duration_seconds",
		Help:    "Private MCP request latency by fixed protocol method and bounded outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "outcome"})
	charlieMCPListenerActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_charlie_mcp_listener_active",
		Help: "Whether this replica is serving the private MCP listener.",
	})
	charlieMCPListenerEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_charlie_mcp_listener_events_total",
		Help: "Private MCP listener lifecycle events using fixed vocabulary.",
	}, []string{"event"})
	charlieExpirySeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_charlie_expiry_seconds",
		Help: "Seconds until bounded Charlie integration material expires; negative values are expired.",
	}, []string{"kind"})
)

func init() {
	for _, collector := range []prometheus.Collector{charlieBridgeCalls, charlieBridgeDuration, charlieActions, charlieTriggers, charlieActivation, charlieEffectiveMode, charlieModeDrift, charlieSSEConnections, charlieSSEEvents, charlieMCPCalls, charlieMCPDuration, charlieMCPListenerActive, charlieMCPListenerEvents, charlieExpirySeconds} {
		if err := prometheus.Register(collector); err != nil {
			if _, already := err.(prometheus.AlreadyRegisteredError); !already {
				panic(err)
			}
		}
	}
}

func observeConnectionExpiries(connection sqlc.CharlieConnection, now time.Time) {
	expiries := map[string]time.Time{
		"certificate":        connection.CertificateExpiresAt,
		"enrollment":         connection.EnrollmentCredentialsExpiresAt,
		"artifact":           connection.ArtifactCredentialExpiresAt,
		"onboarding_package": connection.OnboardingPackageExpiresAt,
	}
	for kind, expiresAt := range expiries {
		value := math.NaN()
		if !expiresAt.IsZero() {
			value = expiresAt.Sub(now.UTC()).Seconds()
		}
		charlieExpirySeconds.WithLabelValues(kind).Set(value)
	}
}

func observeStreamOpened(resumed bool) {
	charlieSSEConnections.Inc()
	charlieSSEEvents.WithLabelValues("opened").Inc()
	if resumed {
		charlieSSEEvents.WithLabelValues("resumed").Inc()
	}
}

func observeStreamClosed(failed bool) {
	charlieSSEConnections.Dec()
	event := "closed"
	if failed {
		event = "failed"
	}
	charlieSSEEvents.WithLabelValues(event).Inc()
}

func observeStreamAcknowledgement(success bool) {
	event := "acknowledged"
	if !success {
		event = "ack_failed"
	}
	charlieSSEEvents.WithLabelValues(event).Inc()
}

func observeModeDrift(requested, verified Mode, active bool) {
	value := 0.0
	if active && validMode(requested) && validMode(verified) && requested != verified {
		value = 1
	}
	charlieModeDrift.Set(value)
}

func observeMCPCall(method, outcome string, started time.Time) {
	method = mcpMethodLabel(method)
	outcome = mcpOutcomeLabel(outcome)
	charlieMCPCalls.WithLabelValues(method, outcome).Inc()
	charlieMCPDuration.WithLabelValues(method, outcome).Observe(time.Since(started).Seconds())
}

func observeMCPListener(event string) {
	switch event {
	case "activated", "restarted", "deactivated", "serve_failed":
	default:
		event = "other"
	}
	charlieMCPListenerEvents.WithLabelValues(event).Inc()
}

func mcpMethodLabel(value string) string {
	switch value {
	case "initialize":
		return "initialize"
	case "notifications/initialized":
		return "initialized"
	case "tools/list":
		return "tools_list"
	case "tools/call":
		return "tools_call"
	default:
		return "unknown"
	}
}

func mcpOutcomeLabel(value string) string {
	switch value {
	case "success", "inactive", "unauthorized", "denied", "invalid", "not_found", "failed":
		return value
	default:
		return "failed"
	}
}

func observeEffectiveMode(mode Mode) {
	if !validMode(mode) {
		mode = ModeDisabled
	}
	for _, candidate := range []Mode{ModeDisabled, ModeReadOnly, ModeApproval, ModeAuto} {
		value := 0.0
		if mode == candidate {
			value = 1
		}
		charlieEffectiveMode.WithLabelValues(string(candidate)).Set(value)
	}
}

func observeActivation(state ActivationState) {
	for _, candidate := range []ActivationState{
		ActivationFeatureDisabled, ActivationUnconfigured, ActivationInstalling,
		ActivationEmergencyStop, ActivationInactive, ActivationReady,
	} {
		value := 0.0
		if state == candidate {
			value = 1
		}
		charlieActivation.WithLabelValues(string(candidate)).Set(value)
	}
}

func observeBridgeCall(operation string, started time.Time, err error) {
	operation = bridgeOperationLabel(operation)
	outcome := "success"
	if err != nil {
		outcome = "failed"
		var stable *contract.StableError
		if errors.As(err, &stable) {
			outcome = bridgeOutcomeLabel(stable.Code)
		}
	}
	charlieBridgeCalls.WithLabelValues(operation, outcome).Inc()
	charlieBridgeDuration.WithLabelValues(operation, outcome).Observe(time.Since(started).Seconds())
}

func observeAction(envelope ActionEnvelope, result ActionResult) {
	descriptor, found := capabilityByName(envelope.Capability)
	capability := "unknown"
	effect := "unknown"
	if found {
		capability = descriptor.Name
		effect = string(descriptor.Effect)
	}
	state := actionStateLabel(result.State)
	code := "none"
	if result.Code != "" {
		code = denialCodeLabel(string(result.Code))
	}
	charlieActions.WithLabelValues(capability, effect, state, code).Inc()
}

func observeTrigger(rule, outcome string) {
	charlieTriggers.WithLabelValues(triggerRuleLabel(rule), triggerOutcomeLabel(outcome)).Inc()
}

func bridgeOperationLabel(value string) string {
	switch value {
	case "create_session", "get_session", "get_history", "create_message", "abort_session", "stream_events", "create_investigation", "get_finding", "transition_finding":
		return value
	default:
		return "unknown"
	}
}

func bridgeOutcomeLabel(value string) string {
	switch value {
	case "integration_inactive", "bridge_circuit_open", "bridge_timeout", "bridge_unavailable", "bridge_unauthenticated", "bridge_forbidden", "bridge_conflict", "bridge_rate_limited", "bridge_rejected", "invalid_bridge_response", "authorization_ref_required":
		return value
	default:
		return "failed"
	}
}

func actionStateLabel(value string) string {
	switch value {
	case "blocked", "dispatched", "verifying", "succeeded", "failed", "ambiguous":
		return value
	default:
		return "unknown"
	}
}

func denialCodeLabel(value string) string {
	for _, code := range []DenialCode{
		DeniedFeatureDisabled, DeniedConnectionInactive, DeniedEmergencyDisabled,
		DeniedModeDisabled, DeniedDestructive, DeniedDisclosureChanged, DeniedAuthorization,
		DeniedReadOnlyWrite,
		DeniedApprovalRequired, DeniedApprovalInvalid,
		DeniedNotAutoEligible, DeniedNotAllowlisted, DeniedScope,
		DeniedBudget, DeniedCooldown, DeniedCircuitOpen, DeniedPrecondition,
		DeniedIdempotency, DeniedVerification, DeniedStaleFencing,
		DeniedAmbiguousPriorAttempt,
	} {
		if value == string(code) {
			return value
		}
	}
	return "other"
}

func triggerRuleLabel(value string) string {
	for _, rule := range DefaultTriggerRules() {
		if value == rule.Name {
			return value
		}
	}
	return "custom"
}

func triggerOutcomeLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "inactive", "filtered", "scheduled", "coalesced", "suppressed", "dispatched", "retry", "dead":
		return value
	default:
		return "other"
	}
}
