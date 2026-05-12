package registration

import (
	"errors"
	"testing"
)

// TestRegistrationWizard_PhaseTransitionsCreatedToReady walks the happy
// path with no baseline: created → awaiting_agent → connected → ready.
func TestRegistrationWizard_PhaseTransitionsCreatedToReady(t *testing.T) {
	cases := []struct {
		name string
		from Phase
		ev   Event
		want Phase
	}{
		{"created->awaiting_agent on confirm", PhaseCreated, EventConfirm, PhaseAwaitingAgent},
		{"awaiting->connected on heartbeat", PhaseAwaitingAgent, EventAgentConnected, PhaseConnected},
		{"connected->ready when no baseline", PhaseConnected, EventNoProvisioning, PhaseReady},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Transition(c.from, c.ev, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

// TestRegistrationWizard_PhaseTransitionsWithBaseline walks the path
// that exercises the apply worker: connected → provisioning → ready.
func TestRegistrationWizard_PhaseTransitionsWithBaseline(t *testing.T) {
	if got, err := Transition(PhaseConnected, EventTemplateApplying, true); err != nil || got != PhaseProvisioning {
		t.Fatalf("connected+template_applying: got=%s err=%v", got, err)
	}
	if got, err := Transition(PhaseProvisioning, EventTemplateApplied, true); err != nil || got != PhaseReady {
		t.Fatalf("provisioning+template_applied: got=%s err=%v", got, err)
	}
}

// TestRegistrationWizard_FailedStepTransitionsToFailed checks that the
// terminal failure edge is wired.
func TestRegistrationWizard_FailedStepTransitionsToFailed(t *testing.T) {
	got, err := Transition(PhaseProvisioning, EventTemplateFailed, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PhaseFailed {
		t.Fatalf("want failed, got %s", got)
	}
}

// TestRegistrationWizard_IllegalTransitionsRejected checks that
// out-of-order events return ErrIllegalTransition rather than silently
// no-op'ing — the API layer surfaces these as 409 Conflict.
func TestRegistrationWizard_IllegalTransitionsRejected(t *testing.T) {
	for _, c := range []struct {
		from Phase
		ev   Event
	}{
		{PhaseCreated, EventAgentConnected},         // skipping confirm
		{PhaseCreated, EventTemplateApplying},       // template before agent
		{PhaseAwaitingAgent, EventTemplateApplied},  // template before connect
		{PhaseReady, EventConfirm},                  // re-confirming a ready cluster
		{PhaseFailed, EventConfirm},                 // re-confirming a failed cluster
		{PhaseReady, EventCancel},                   // cancel from terminal
		{PhaseConnected, EventRetry},                // retry from non-failed
	} {
		if _, err := Transition(c.from, c.ev, false); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("%s+%s should be illegal, got err=%v", c.from, c.ev, err)
		}
	}
}

// TestRegistrationWizard_RetryFailedStep checks that the retry edge
// rewinds from failed back to provisioning so the apply worker can
// be re-invoked.
func TestRegistrationWizard_RetryFailedStep(t *testing.T) {
	got, err := Transition(PhaseFailed, EventRetry, true)
	if err != nil {
		t.Fatalf("retry from failed: %v", err)
	}
	if got != PhaseProvisioning {
		t.Fatalf("retry should rewind to provisioning, got %s", got)
	}
}

// TestRegistrationWizard_CancelFromTerminalRejected — the wizard's
// cancel endpoint is restricted to in-flight phases. A ready/failed
// cluster doesn't need cancelling and accepting one would silently
// rewrite the row.
func TestRegistrationWizard_CancelFromTerminalRejected(t *testing.T) {
	for _, p := range []Phase{PhaseReady, PhaseFailed} {
		if _, err := Transition(p, EventCancel, false); !errors.Is(err, ErrIllegalTransition) {
			t.Errorf("cancel from %s should be illegal, got %v", p, err)
		}
	}
}

// TestRegistrationWizard_CancelMidFlowAllowed — every in-flight phase
// must be cancellable so a superuser can recover a stuck cluster.
func TestRegistrationWizard_CancelMidFlowAllowed(t *testing.T) {
	for _, p := range []Phase{PhaseCreated, PhaseAwaitingAgent, PhaseConnected, PhaseProvisioning} {
		got, err := Transition(p, EventCancel, false)
		if err != nil {
			t.Errorf("cancel from %s should be allowed: %v", p, err)
		}
		if got != PhaseFailed {
			t.Errorf("cancel from %s -> want failed, got %s", p, got)
		}
	}
}

// TestRegistrationWizard_IdempotentHeartbeat — receiving the same
// connect event when already in `connected` should be a no-op so a
// re-connecting agent doesn't flap the wizard URL.
func TestRegistrationWizard_IdempotentHeartbeat(t *testing.T) {
	got, err := Transition(PhaseConnected, EventAgentConnected, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PhaseConnected {
		t.Fatalf("idempotent heartbeat should stay connected, got %s", got)
	}
}

// TestRegistrationWizard_IsTerminal — sanity-check the helper used by
// the cluster-detail panel to decide whether to collapse the
// Provisioning tab.
func TestRegistrationWizard_IsTerminal(t *testing.T) {
	if !IsTerminal(PhaseReady) || !IsTerminal(PhaseFailed) {
		t.Error("ready and failed should be terminal")
	}
	for _, p := range []Phase{PhaseCreated, PhaseAwaitingAgent, PhaseConnected, PhaseProvisioning} {
		if IsTerminal(p) {
			t.Errorf("%s should NOT be terminal", p)
		}
	}
}

// TestRegistrationWizard_StepLabel — the server-rendered labels keep
// copy consistent between wizard page 3 and the Provisioning tab.
func TestRegistrationWizard_StepLabel(t *testing.T) {
	cases := map[string]string{
		"cluster_created":             "Cluster created",
		"manifest_generated":          "Manifest generated",
		"agent_connected":             "Agent connected",
		"template_applying":           "Applying Platform Baseline",
		"template_applied":            "Platform Baseline applied",
		"template_failed":             "Platform Baseline failed",
		"no_provisioning":             "Skipped Platform Baseline (operator opted out)",
		"tool_installing:trivy-operator": "Installing tool: trivy-operator",
		"tool_installed:fluent-bit":      "Installed tool: fluent-bit",
		"tool_failed:cert-manager":       "Failed to install tool: cert-manager",
	}
	for k, want := range cases {
		if got := StepLabel(k); got != want {
			t.Errorf("StepLabel(%q) = %q, want %q", k, got, want)
		}
	}
}
