// Package registration owns the server-authoritative phase state machine
// that drives the Rancher-style cluster-registration wizard.
//
// The state machine is intentionally small — six terminal/intermediate
// phases — and the transitions are gated on observed events rather than
// operator clicks where possible. The frontend never INVENTS state; it
// reads the current phase from the API and routes the wizard URL
// accordingly. A refresh mid-flow always lands on the same screen.
//
// Phase graph:
//
//	  created
//	     |  POST /confirm/  (operator clicks "I've run it")
//	     v
//	awaiting_agent
//	     |  first agent.connected heartbeat
//	     v
//	  connected
//	     |
//	     +-- install_baseline=false ---> ready
//	     |
//	     +-- install_baseline=true  ---> provisioning
//	                                        |
//	                                        +-- all tools applied --> ready
//	                                        |
//	                                        +-- all retries spent  --> failed
//
// Any retry-exhausted failure → failed; the operator can manually retry
// the failing step via POST /retry/.
package registration

import (
	"errors"
	"fmt"
)

// Phase is the registration state. Mirrors the CHECK constraint on
// clusters.registration_phase.
type Phase string

const (
	PhaseCreated       Phase = "created"
	PhaseAwaitingAgent Phase = "awaiting_agent"
	PhaseConnected     Phase = "connected"
	PhaseProvisioning  Phase = "provisioning"
	PhaseReady         Phase = "ready"
	PhaseFailed        Phase = "failed"
)

// Event is a transition trigger. Names spelled to match the audit log
// entries / SSE event types the rest of the system emits.
type Event string

const (
	EventConfirm          Event = "confirm"           // POST /registration/confirm/
	EventAgentConnected   Event = "agent_connected"   // first heartbeat
	EventTemplateApplying Event = "template_applying" // cluster_template:apply task started
	EventTemplateApplied  Event = "template_applied"  // task completed clean
	EventTemplateFailed   Event = "template_failed"   // task ran out of retries
	EventNoProvisioning   Event = "no_provisioning"   // connected w/ install_baseline=false
	EventCancel           Event = "cancel"            // superuser abort
	EventRetry            Event = "retry"             // operator clicked retry on a failed step
)

// ErrIllegalTransition is returned by Transition when the (current,event)
// pair does not have a defined next phase. The caller surfaces this as
// 409 Conflict to the API; the state machine never mutates the row when
// this happens.
var ErrIllegalTransition = errors.New("illegal phase transition")

// Transition returns the next phase given the current phase + event,
// or ErrIllegalTransition if the pair has no edge. The `baseline`
// parameter is only consulted when transitioning out of `connected` —
// it decides whether to head into `provisioning` (true) or skip
// straight to `ready` (false). The parameter is ignored otherwise.
func Transition(current Phase, ev Event, baseline bool) (Phase, error) {
	switch ev {
	case EventCancel:
		// Superuser can always abort except from a terminal state.
		if current == PhaseReady || current == PhaseFailed {
			return current, fmt.Errorf("%w: cancel from %s", ErrIllegalTransition, current)
		}
		return PhaseFailed, nil
	case EventRetry:
		// Retry rewinds from `failed` back to `provisioning`. Other
		// states never need a retry — they advance via the natural
		// triggers.
		if current != PhaseFailed {
			return current, fmt.Errorf("%w: retry from %s", ErrIllegalTransition, current)
		}
		return PhaseProvisioning, nil
	}

	switch current {
	case PhaseCreated:
		if ev == EventConfirm {
			return PhaseAwaitingAgent, nil
		}
	case PhaseAwaitingAgent:
		if ev == EventAgentConnected {
			return PhaseConnected, nil
		}
	case PhaseConnected:
		switch ev {
		case EventTemplateApplying:
			return PhaseProvisioning, nil
		case EventNoProvisioning:
			return PhaseReady, nil
		case EventAgentConnected:
			// Idempotent: re-receiving the heartbeat for a cluster
			// already in `connected` is a no-op rather than a
			// failure. Lets the agent retry without flapping.
			return PhaseConnected, nil
		}
	case PhaseProvisioning:
		switch ev {
		case EventTemplateApplied:
			return PhaseReady, nil
		case EventTemplateFailed:
			return PhaseFailed, nil
		}
	case PhaseFailed:
		// Self-healing edges out of `failed`. Until sprint 086 these
		// were strictly terminal, but the orchestrator's auto-retry
		// loop fires EventTemplateApplying again on each attempt
		// without an explicit operator-driven retry, so a cluster
		// whose tool install eventually succeeds would stay stuck on
		// `failed` even though the agent finished cleanly. Treating
		// these signals as implicit retries lets the phase column
		// track reality instead of getting frozen on the first
		// transient.
		switch ev {
		case EventTemplateApplying:
			return PhaseProvisioning, nil
		case EventTemplateApplied:
			// A success delivered while we're still PhaseFailed (the
			// orchestrator skipped the intermediate Applying step
			// because it had one in-flight). Jump straight to Ready.
			return PhaseReady, nil
		case EventAgentConnected:
			// Agent reconnect after a fail — get back to Connected
			// so the wizard's "agent online" indicator clears.
			return PhaseConnected, nil
		}
	case PhaseReady:
		// Terminal end-state. Only cancel (handled above) can move
		// off it, and ready specifically doesn't accept cancel.
	}
	_ = baseline // suppress unused warning when callers omit it from non-baseline events
	return current, fmt.Errorf("%w: %s + %s", ErrIllegalTransition, current, ev)
}

// IsTerminal reports whether a phase is end-of-flow. Used by the SSE
// fan-out to know when to drop the subscription, and by the cluster
// detail view to collapse the Provisioning tab by default.
func IsTerminal(p Phase) bool {
	return p == PhaseReady || p == PhaseFailed
}

// Valid reports whether p is one of the recognized phase values.
// Mirrors the DB CHECK constraint; used by the API handlers to reject
// malformed updates before they reach Postgres.
func Valid(p Phase) bool {
	switch p {
	case PhaseCreated, PhaseAwaitingAgent, PhaseConnected, PhaseProvisioning, PhaseReady, PhaseFailed:
		return true
	}
	return false
}

// StepLabel maps a machine step_name to the human-readable label we
// stamp into cluster_registration_steps.label at insert time. The
// frontend renders the column verbatim; centralising the mapping here
// keeps copy consistent between wizard page 3 and the Provisioning tab.
func StepLabel(stepName string) string {
	// Tool-specific install steps carry a `tool_installing:<slug>` /
	// `tool_installed:<slug>` shape. Split off the suffix so the label
	// stays generic ("Installing tool: trivy-operator") rather than
	// requiring a per-tool lookup.
	if prefix, suffix, ok := splitColon(stepName); ok {
		switch prefix {
		case "tool_installing":
			return "Installing tool: " + suffix
		case "tool_installed":
			return "Installed tool: " + suffix
		case "tool_failed":
			return "Failed to install tool: " + suffix
		}
	}
	switch stepName {
	case "cluster_created":
		return "Cluster created"
	case "manifest_generated":
		return "Manifest generated"
	case "agent_connected":
		return "Agent connected"
	case "template_applying":
		return "Applying Platform Baseline"
	case "template_applied":
		return "Platform Baseline applied"
	case "template_failed":
		return "Platform Baseline failed"
	case "argocd_registering":
		return "Registering cluster in ArgoCD"
	case "argocd_registered":
		return "Cluster registered in ArgoCD"
	case "argocd_registration_failed":
		return "ArgoCD registration failed"
	case "baseline_appsets_matched":
		return "Baseline ApplicationSets matched"
	case "no_provisioning":
		return "Skipped Platform Baseline (operator opted out)"
	case "cancelled":
		return "Registration cancelled"
	}
	return stepName
}

func splitColon(s string) (prefix, suffix string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
