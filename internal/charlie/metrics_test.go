package charlie

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCharlieMetricLabelsAreFixedVocabulary(t *testing.T) {
	for _, operation := range []string{"list_approvals", "decide_approval"} {
		if got := bridgeOperationLabel(operation); got != operation {
			t.Fatalf("known bridge operation %q became %q", operation, got)
		}
	}
	if got := bridgeOperationLabel("prompt-secret-canary"); got != "unknown" {
		t.Fatalf("untrusted bridge operation became a label: %q", got)
	}
	if got := bridgeOutcomeLabel("upstream-secret-canary"); got != "failed" {
		t.Fatalf("untrusted bridge error became a label: %q", got)
	}
	if got := denialCodeLabel("model-secret-canary"); got != "other" {
		t.Fatalf("untrusted denial became a label: %q", got)
	}
	if got := triggerRuleLabel("tenant-secret-canary"); got != "custom" {
		t.Fatalf("custom trigger name became a label: %q", got)
	}
	if got := mcpMethodLabel("tools/secret-canary"); got != "unknown" {
		t.Fatalf("untrusted MCP method became a label: %q", got)
	}
	if got := mcpOutcomeLabel("secret-error-body"); got != "failed" {
		t.Fatalf("untrusted MCP outcome became a label: %q", got)
	}
}

func TestCharlieCounterFamiliesExistAtZero(t *testing.T) {
	collectors := map[string]prometheus.Collector{
		"bridge":   charlieBridgeCalls,
		"actions":  charlieActions,
		"triggers": charlieTriggers,
		"sse":      charlieSSEEvents,
		"mcp":      charlieMCPCalls,
		"listener": charlieMCPListenerEvents,
	}
	for name, collector := range collectors {
		if got := testutil.CollectAndCount(collector); got == 0 {
			t.Fatalf("%s counter family is absent before its first event", name)
		}
	}
}

func TestCharlieExpiryMetricsUseFixedKindsAndExactDeadlines(t *testing.T) {
	now := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	connection := sqlc.CharlieConnection{
		CertificateExpiresAt:           now.Add(7 * 24 * time.Hour),
		EnrollmentCredentialsExpiresAt: now.Add(15 * time.Minute),
		ArtifactCredentialExpiresAt:    now.Add(6 * time.Hour),
		OnboardingPackageExpiresAt:     now.Add(-time.Minute),
	}
	observeConnectionExpiries(connection, now)
	wants := map[string]float64{
		"certificate":        7 * 24 * 60 * 60,
		"enrollment":         15 * 60,
		"artifact":           6 * 60 * 60,
		"onboarding_package": -60,
	}
	for kind, want := range wants {
		if got := testutil.ToFloat64(charlieExpirySeconds.WithLabelValues(kind)); got != want {
			t.Fatalf("expiry %s=%v, want %v", kind, got, want)
		}
	}
	observeConnectionExpiries(sqlc.CharlieConnection{}, now)
	for kind := range wants {
		if got := testutil.ToFloat64(charlieExpirySeconds.WithLabelValues(kind)); !math.IsNaN(got) {
			t.Fatalf("missing expiry %s=%v, want NaN", kind, got)
		}
	}
}

func TestCharlieMetricsRecordOnlyNormalizedOutcomes(t *testing.T) {
	bridge := charlieBridgeCalls.WithLabelValues("get_session", "bridge_timeout")
	beforeBridge := testutil.ToFloat64(bridge)
	observeBridgeCall("get_session", time.Now(), &contract.StableError{Code: "bridge_timeout"})
	if after := testutil.ToFloat64(bridge); after != beforeBridge+1 {
		t.Fatalf("bridge metric delta = %v, want 1", after-beforeBridge)
	}

	action := charlieActions.WithLabelValues("unknown", "unknown", "blocked", "authorization_denied")
	beforeAction := testutil.ToFloat64(action)
	observeAction(ActionEnvelope{Capability: "secret-capability"}, denied(DeniedAuthorization, "not exported"))
	if after := testutil.ToFloat64(action); after != beforeAction+1 {
		t.Fatalf("action metric delta = %v, want 1", after-beforeAction)
	}

	unknown := charlieBridgeCalls.WithLabelValues("unknown", "failed")
	beforeUnknown := testutil.ToFloat64(unknown)
	observeBridgeCall("secret-operation", time.Now(), errors.New("secret error body"))
	if after := testutil.ToFloat64(unknown); after != beforeUnknown+1 {
		t.Fatalf("unknown bridge metric delta = %v, want 1", after-beforeUnknown)
	}

	mcp := charlieMCPCalls.WithLabelValues("unknown", "failed")
	beforeMCP := testutil.ToFloat64(mcp)
	observeMCPCall("secret-method", "secret-error-body", time.Now())
	if after := testutil.ToFloat64(mcp); after != beforeMCP+1 {
		t.Fatalf("MCP metric delta = %v, want 1", after-beforeMCP)
	}
}

func TestCharlieActivationMetricIsOneHot(t *testing.T) {
	observeActivation(ActivationEmergencyStop)
	for _, state := range []ActivationState{
		ActivationFeatureDisabled, ActivationUnconfigured, ActivationInstalling,
		ActivationEmergencyStop, ActivationInactive, ActivationReady,
	} {
		want := 0.0
		if state == ActivationEmergencyStop {
			want = 1
		}
		if got := testutil.ToFloat64(charlieActivation.WithLabelValues(string(state))); got != want {
			t.Fatalf("activation state %s = %v, want %v", state, got, want)
		}
	}
	observeEffectiveMode(ModeApproval)
	for _, mode := range []Mode{ModeDisabled, ModeReadOnly, ModeApproval, ModeAuto} {
		want := 0.0
		if mode == ModeApproval {
			want = 1
		}
		if got := testutil.ToFloat64(charlieEffectiveMode.WithLabelValues(string(mode))); got != want {
			t.Fatalf("effective mode %s = %v, want %v", mode, got, want)
		}
	}
	observeModeDrift(ModeApproval, ModeReadOnly, true)
	if got := testutil.ToFloat64(charlieModeDrift); got != 1 {
		t.Fatalf("mode drift = %v, want 1", got)
	}
	observeModeDrift(ModeApproval, ModeReadOnly, false)
	if got := testutil.ToFloat64(charlieModeDrift); got != 0 {
		t.Fatalf("inactive mode drift = %v, want 0", got)
	}
}
