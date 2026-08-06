package charlie

import (
	"slices"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestFindingWorkflowHasOneBoundedDecisionSetForEveryNonExecutionReason(t *testing.T) {
	now := time.Unix(10_000, 0).UTC()
	manualReasons := []string{
		"read_only", "read_only_write", "approval_invalid", "not_auto_eligible", "non_auto_eligible",
		"not_allowlisted", "allowlist_denied", "authorization_denied", "product_rbac_denied",
		"scope_denied", "budget_exhausted", "safety_budget_exceeded", "cooldown_active",
		"precondition_failed", "circuit_open", "circuit_breaker_open", "disclosure_changed",
		"disclosure_drift", "stale_fencing_epoch", "stale_leadership", "idempotency_required",
		"ambiguous_prior_attempt", "central_unavailable", "no_safe_action", "capability_destructive",
		"destructive_capability", "execution_failed",
	}
	for _, reason := range manualReasons {
		t.Run(reason, func(t *testing.T) {
			workflow := FindingWorkflowFor(sqlc.CharlieFinding{Status: "open", EffectiveMode: string(ModeReadOnly), ExecutionBlockCode: reason}, now)
			if workflow.State != FindingWorkflowManualRemediationRequired || len(workflow.Decisions) != 3 ||
				!FindingWorkflowAllows(sqlc.CharlieFinding{Status: "open", EffectiveMode: string(ModeReadOnly), ExecutionBlockCode: reason}, "dismiss", now) {
				t.Fatalf("workflow = %#v", workflow)
			}
		})
	}
	verification := FindingWorkflowFor(sqlc.CharlieFinding{Status: "open", EffectiveMode: string(ModeAuto), ExecutionBlockCode: "verification_failed"}, now)
	if verification.State != FindingWorkflowVerificationPending || !slices.Contains(verification.Decisions, "start_remediation") || !slices.Contains(verification.Decisions, "resolve") {
		t.Fatalf("verification workflow = %#v", verification)
	}
}

func TestFindingWorkflowUsesPersistedCentralWorkflowWithoutInferringAuthority(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		state    FindingWorkflowState
		decision string
	}{
		{FindingWorkflowManualRemediationRequired, "start_remediation"},
		{FindingWorkflowRemediationInProgress, "request_verification"},
		{FindingWorkflowVerificationPending, "resolve"},
	}
	for _, test := range tests {
		row := sqlc.CharlieFinding{Status: "acknowledged", EffectiveMode: string(ModeReadOnly), WorkflowState: string(test.state), ExecutionBlockCode: "read_only"}
		workflow := FindingWorkflowFor(row, now)
		if workflow.State != test.state || !slices.Contains(workflow.Decisions, test.decision) {
			t.Fatalf("state %s produced %#v", test.state, workflow)
		}
	}
}

func TestFindingWorkflowApprovalExposesPendingStateWithoutExactLink(t *testing.T) {
	now := time.Unix(20_000, 0).UTC()
	row := sqlc.CharlieFinding{Status: "open", ExecutionBlockCode: "approval_required",
		EffectiveMode: string(ModeApproval),
		WorkflowState: string(FindingWorkflowApprovalPending),
		ExpiresAt:     pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}}
	workflow := FindingWorkflowFor(row, now)
	if workflow.State != FindingWorkflowApprovalPending || len(workflow.Decisions) != 0 || FindingWorkflowAllows(row, "resolve", now) {
		t.Fatalf("approval workflow = %#v", workflow)
	}
	row.ExpiresAt.Time = now.Add(-time.Second)
	if got := FindingWorkflowFor(row, now); got.State != FindingWorkflowExpired || len(got.Decisions) != 0 {
		t.Fatalf("expired approval retained authority: %#v", got)
	}
	row.ExecutionBlockCode = string(ReasonApprovalExpired)
	row.WorkflowState = string(FindingWorkflowManualRemediationRequired)
	if got := FindingWorkflowFor(row, now); got.State != FindingWorkflowManualRemediationRequired ||
		!FindingWorkflowAllows(row, "start_remediation", now) {
		t.Fatalf("persisted expired approval did not offer safe manual remediation: %#v", got)
	}
	row.ExecutionBlockCode = string(ReasonApprovalRequired)
	row.WorkflowState = ""
	row.ExpiresAt.Time = now.Add(time.Minute)
	row.ExpiresAt = pgtype.Timestamptz{}
	if got := FindingWorkflowFor(row, now); got.State != FindingWorkflowApprovalPending || len(got.Decisions) != 0 {
		t.Fatalf("approval state depended on exact approval linkage: %#v", got)
	}
}

func TestFindingWorkflowTerminalAndLifecycleStatesHaveNoHiddenAuthority(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		row  sqlc.CharlieFinding
		want FindingWorkflowState
	}{
		{sqlc.CharlieFinding{Status: "acknowledged", EffectiveMode: string(ModeReadOnly), ExecutionBlockCode: "read_only"}, FindingWorkflowRemediationInProgress},
		{sqlc.CharlieFinding{Status: "resolved", ExecutionBlockCode: "read_only"}, FindingWorkflowResolved},
		{sqlc.CharlieFinding{Status: "resolved", ExecutionBlockCode: "approval_rejected"}, FindingWorkflowRejected},
		{sqlc.CharlieFinding{Status: "dismissed"}, FindingWorkflowDismissed},
		{sqlc.CharlieFinding{Status: "expired"}, FindingWorkflowExpired},
	}
	for _, test := range tests {
		got := FindingWorkflowFor(test.row, now)
		if got.State != test.want {
			t.Fatalf("row=%#v workflow=%#v want=%s", test.row, got, test.want)
		}
		if test.row.Status != "acknowledged" && len(got.Decisions) != 0 {
			t.Fatalf("terminal workflow exposed decisions: %#v", got)
		}
	}
}
