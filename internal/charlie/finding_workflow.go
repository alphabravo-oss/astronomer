package charlie

import (
	"slices"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type FindingWorkflowState string

const (
	FindingWorkflowApprovalPending           FindingWorkflowState = "approval_pending"
	FindingWorkflowManualRemediationRequired FindingWorkflowState = "manual_remediation_required"
	FindingWorkflowRemediationInProgress     FindingWorkflowState = "remediation_in_progress"
	FindingWorkflowVerificationPending       FindingWorkflowState = "verification_pending"
	FindingWorkflowResolved                  FindingWorkflowState = "resolved"
	FindingWorkflowRejected                  FindingWorkflowState = "rejected"
	FindingWorkflowDismissed                 FindingWorkflowState = "dismissed"
	FindingWorkflowExpired                   FindingWorkflowState = "expired"
)

type FindingWorkflow struct {
	State     FindingWorkflowState `json:"state"`
	Decisions []string             `json:"decisions"`
}

// FindingWorkflowFor derives the one legal operator workflow from durable,
// product-owned finding metadata. Central/model content cannot add a decision.
func FindingWorkflowFor(row sqlc.CharlieFinding, now time.Time) FindingWorkflow {
	switch row.Status {
	case "dismissed":
		return noDecisionFindingWorkflow(FindingWorkflowDismissed)
	case "expired":
		return noDecisionFindingWorkflow(FindingWorkflowExpired)
	case "resolved":
		if row.ExecutionBlockCode == string(ReasonApprovalRejected) {
			return noDecisionFindingWorkflow(FindingWorkflowRejected)
		}
		return noDecisionFindingWorkflow(FindingWorkflowResolved)
	}
	reason := DenialCode(row.ExecutionBlockCode)
	reason, normalized := NormalizeNonExecutionReason(reason)
	if !normalized || !IsActionableNonExecutionReason(reason) || !activeFindingMode(Mode(row.EffectiveMode)) {
		return noDecisionFindingWorkflow(FindingWorkflowManualRemediationRequired)
	}
	if reason == ReasonApprovalExpired {
		return manualFindingWorkflow(row.Status)
	}
	if reason == ReasonApprovalRejected {
		return manualFindingWorkflow(row.Status)
	}
	if row.WorkflowState == string(FindingWorkflowApprovalPending) || (row.WorkflowState == "" && reason == ReasonApprovalRequired) {
		if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(now.UTC()) {
			return noDecisionFindingWorkflow(FindingWorkflowExpired)
		}
		if reason != ReasonApprovalRequired ||
			(Mode(row.EffectiveMode) != ModeApproval && Mode(row.EffectiveMode) != ModeAuto) {
			return manualFindingWorkflow(row.Status)
		}
		// A finding exposes only advisory state. Exact approval identifiers and
		// decisions are discovered and performed through the approvals lane.
		return noDecisionFindingWorkflow(FindingWorkflowApprovalPending)
	}
	switch FindingWorkflowState(row.WorkflowState) {
	case FindingWorkflowManualRemediationRequired:
		return manualFindingWorkflow(row.Status)
	case FindingWorkflowRemediationInProgress:
		return FindingWorkflow{State: FindingWorkflowRemediationInProgress,
			Decisions: []string{"request_verification", "dismiss"}}
	case FindingWorkflowVerificationPending:
		return FindingWorkflow{State: FindingWorkflowVerificationPending,
			Decisions: []string{"start_remediation", "dismiss", "resolve"}}
	}
	if reason == ReasonVerificationFailed {
		return FindingWorkflow{State: FindingWorkflowVerificationPending,
			Decisions: []string{"start_remediation", "dismiss", "resolve"}}
	}
	if row.Status == "acknowledged" {
		return FindingWorkflow{State: FindingWorkflowRemediationInProgress,
			Decisions: []string{"request_verification", "dismiss"}}
	}
	return FindingWorkflow{State: FindingWorkflowManualRemediationRequired,
		Decisions: []string{"acknowledge", "start_remediation", "dismiss"}}
}

func manualFindingWorkflow(status string) FindingWorkflow {
	decisions := []string{"start_remediation", "dismiss"}
	if status == "open" || status == "reopened" {
		decisions = append([]string{"acknowledge"}, decisions...)
	}
	return FindingWorkflow{State: FindingWorkflowManualRemediationRequired, Decisions: decisions}
}

func activeFindingMode(mode Mode) bool {
	return mode == ModeReadOnly || mode == ModeApproval || mode == ModeAuto
}

func noDecisionFindingWorkflow(state FindingWorkflowState) FindingWorkflow {
	return FindingWorkflow{State: state, Decisions: []string{}}
}

func FindingWorkflowAllows(row sqlc.CharlieFinding, decision string, now time.Time) bool {
	return slices.Contains(FindingWorkflowFor(row, now).Decisions, decision)
}
