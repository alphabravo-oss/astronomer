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
		if row.ExecutionBlockCode == "approval_rejected" {
			return noDecisionFindingWorkflow(FindingWorkflowRejected)
		}
		return noDecisionFindingWorkflow(FindingWorkflowResolved)
	}
	if row.ExecutionBlockCode == "approval_expired" {
		return noDecisionFindingWorkflow(FindingWorkflowExpired)
	}
	if row.ExecutionBlockCode == "approval_rejected" {
		return noDecisionFindingWorkflow(FindingWorkflowRejected)
	}
	if row.WorkflowState == string(FindingWorkflowApprovalPending) ||
		(row.WorkflowState == "" && row.ExecutionBlockCode == "approval_required" && row.ApprovalID.Valid) {
		if !row.ExpiresAt.Valid || !row.ExpiresAt.Time.After(now.UTC()) {
			return noDecisionFindingWorkflow(FindingWorkflowExpired)
		}
		if row.ExecutionBlockCode != "approval_required" || !row.ApprovalID.Valid {
			return noDecisionFindingWorkflow(FindingWorkflowExpired)
		}
		return FindingWorkflow{State: FindingWorkflowApprovalPending,
			Decisions: []string{"open_exact_approval", "reject_exact_approval"}}
	}
	switch FindingWorkflowState(row.WorkflowState) {
	case FindingWorkflowManualRemediationRequired:
		decisions := []string{"start_remediation", "dismiss"}
		if row.Status == "open" {
			decisions = append([]string{"acknowledge"}, decisions...)
		}
		return FindingWorkflow{State: FindingWorkflowManualRemediationRequired, Decisions: decisions}
	case FindingWorkflowRemediationInProgress:
		return FindingWorkflow{State: FindingWorkflowRemediationInProgress,
			Decisions: []string{"request_verification", "dismiss"}}
	case FindingWorkflowVerificationPending:
		return FindingWorkflow{State: FindingWorkflowVerificationPending,
			Decisions: []string{"start_remediation", "dismiss", "resolve"}}
	}
	if row.ExecutionBlockCode == "verification_failed" {
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

func noDecisionFindingWorkflow(state FindingWorkflowState) FindingWorkflow {
	return FindingWorkflow{State: state, Decisions: []string{}}
}

func FindingWorkflowAllows(row sqlc.CharlieFinding, decision string, now time.Time) bool {
	return slices.Contains(FindingWorkflowFor(row, now).Decisions, decision)
}
