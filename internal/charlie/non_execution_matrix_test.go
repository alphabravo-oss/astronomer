package charlie

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestA12029A12030NonExecutionWorkflowMatrix(t *testing.T) {
	now := time.Unix(30_000, 0).UTC()
	modes := []Mode{ModeDisabled, ModeReadOnly, ModeApproval, ModeAuto}
	assertA12029Vocabulary(t)

	for _, reason := range boundedNonExecutionReasons {
		for _, mode := range modes {
			t.Run(string(reason)+"/"+string(mode), func(t *testing.T) {
				store := &fakeFindingStore{result: DurableFinding{
					ID: "finding-a", Status: "open", RepeatCount: 1, UpdatedAt: now, Notify: true,
				}}
				publisher := &fakeFindingPublisher{}
				service, err := NewFindingService(store, publisher)
				if err != nil {
					t.Fatal(err)
				}
				input := validFindingInputForTest(reason)
				input.Mode = mode
				input.Decision = AuthorityDecision{Code: reason}
				if _, err := service.RecordBlocked(context.Background(), input); err != nil {
					t.Fatalf("record blocked: %v", err)
				}

				wantDurable := mode != ModeDisabled && IsActionableNonExecutionReason(reason)
				if got := store.calls; got != boolCount(wantDurable) {
					t.Fatalf("durable finding calls=%d want=%d", got, boolCount(wantDurable))
				}
				if got := len(publisher.alerts); got != boolCount(wantDurable) {
					t.Fatalf("alert count=%d want=%d", got, boolCount(wantDurable))
				}
				if wantDurable && publisher.alerts[0].BlockCode != string(reason) {
					t.Fatalf("alert reason=%q want=%q", publisher.alerts[0].BlockCode, reason)
				}

				row := sqlc.CharlieFinding{
					Status: "open", EffectiveMode: string(mode), ExecutionBlockCode: string(reason),
					ApprovalID: pgtype.Text{String: "approval-a", Valid: true},
					ExpiresAt:  pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
				}
				workflow := FindingWorkflowFor(row, now)
				wantState, wantDecisions := expectedNonExecutionWorkflow(reason, mode)
				if workflow.State != wantState || !slices.Equal(workflow.Decisions, wantDecisions) {
					t.Fatalf("workflow=%#v want state=%s decisions=%v", workflow, wantState, wantDecisions)
				}
			})
		}
	}
}

func TestA12029UnknownReasonFailsClosedWithoutDurableWorkOrControl(t *testing.T) {
	now := time.Unix(40_000, 0).UTC()
	store := &fakeFindingStore{result: DurableFinding{Notify: true}}
	publisher := &fakeFindingPublisher{}
	service, _ := NewFindingService(store, publisher)
	input := validFindingInputForTest(DenialCode("upstream_invented_reason"))
	if _, err := service.RecordBlocked(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if store.calls != 0 || len(publisher.alerts) != 0 {
		t.Fatal("unknown reason created durable work or an alert")
	}
	workflow := FindingWorkflowFor(sqlc.CharlieFinding{
		Status: "open", EffectiveMode: string(ModeAuto), ExecutionBlockCode: "upstream_invented_reason",
	}, now)
	if workflow.State != FindingWorkflowManualRemediationRequired || len(workflow.Decisions) != 0 {
		t.Fatalf("unknown reason exposed workflow control: %#v", workflow)
	}
	if validCentralFindingBlockCode("upstream_invented_reason") {
		t.Fatal("central validation accepted an unknown reason")
	}
	missingMode := FindingWorkflowFor(sqlc.CharlieFinding{
		Status: "open", ExecutionBlockCode: string(ReasonApprovalRequired),
		ApprovalID: pgtype.Text{String: "approval-a", Valid: true},
		ExpiresAt:  pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
	}, now)
	if len(missingMode.Decisions) != 0 {
		t.Fatalf("missing effective mode exposed controls: %#v", missingMode)
	}
}

func TestA12029CentralFindingValidationUsesTheSameClosedVocabulary(t *testing.T) {
	for _, reason := range boundedNonExecutionReasons {
		if !validCentralFindingBlockCode(string(reason)) {
			t.Fatalf("central validation rejected bounded reason %q", reason)
		}
		finding := BridgeFindingSummary{Status: "open", WorkflowState: "manual_remediation_required", BlockCode: string(reason)}
		switch reason {
		case ReasonProductDisabled, ReasonDeploymentDisabled:
			if validCentralFindingWorkflow(finding) {
				t.Fatalf("disabled reason %q created active central work", reason)
			}
			finding.Status, finding.WorkflowState = "resolved", "resolved"
		case ReasonApprovalRequired:
			finding.WorkflowState = "approval_pending"
		case ReasonApprovalExpired:
			finding.WorkflowState = "expired"
		case ReasonApprovalRejected:
			finding.Status, finding.WorkflowState = "resolved", "rejected"
		}
		if !validCentralFindingWorkflow(finding) {
			t.Fatalf("central workflow rejected reason=%q row=%#v", reason, finding)
		}
	}
	for _, alias := range []string{"read_only_write", "approval_invalid", "not_auto_eligible", "not_allowlisted",
		"authorization_denied", "budget_exhausted", "circuit_open", "disclosure_changed", "stale_fencing_epoch",
		"idempotency_required", "verification_required", "destructive_capability"} {
		if validCentralFindingBlockCode(alias) {
			t.Fatalf("central validation accepted non-normalized alias %q", alias)
		}
	}
}

func TestA12030CentralDisabledAndTerminalApprovalReasonsCannotCreateActiveWork(t *testing.T) {
	for _, reason := range []DenialCode{ReasonProductDisabled, ReasonDeploymentDisabled} {
		for _, workflow := range []string{"approval_pending", "manual_remediation_required", "remediation_in_progress", "verification_pending"} {
			if validCentralFindingWorkflow(BridgeFindingSummary{Status: "open", WorkflowState: workflow, BlockCode: string(reason)}) {
				t.Fatalf("disabled reason=%s admitted active workflow=%s", reason, workflow)
			}
		}
	}
	for reason, expected := range map[DenialCode]string{
		ReasonApprovalExpired:  "manual_remediation_required",
		ReasonApprovalRejected: "manual_remediation_required",
	} {
		for _, workflow := range []string{"approval_pending", "manual_remediation_required", "remediation_in_progress", "verification_pending"} {
			valid := validCentralFindingWorkflow(BridgeFindingSummary{Status: "acknowledged", WorkflowState: workflow, BlockCode: string(reason)})
			if valid != (workflow == expected) {
				t.Fatalf("terminal approval reason=%s admitted workflow=%s", reason, workflow)
			}
		}
	}
}

func assertA12029Vocabulary(t *testing.T) {
	t.Helper()
	required := []DenialCode{
		ReasonReadOnly, ReasonApprovalRequired, ReasonApprovalRejected, ReasonApprovalExpired,
		ReasonNonAutoEligible, ReasonAllowlistDenied, ReasonProductRBACDenied, ReasonScopeDenied,
		ReasonDisclosureDrift, ReasonSafetyBudgetExceeded, ReasonCooldownActive, ReasonMaintenanceWindow,
		ReasonPreconditionFailed, ReasonCircuitBreakerOpen, ReasonStaleLeadership, ReasonIdempotencyConflict,
		ReasonAmbiguousPriorAttempt, ReasonExecutionFailed, ReasonVerificationFailed,
	}
	seen := make(map[DenialCode]struct{}, len(boundedNonExecutionReasons))
	for _, reason := range boundedNonExecutionReasons {
		if _, duplicate := seen[reason]; duplicate {
			t.Fatalf("duplicate bounded reason %q", reason)
		}
		seen[reason] = struct{}{}
	}
	for _, reason := range required {
		if _, ok := seen[reason]; !ok {
			t.Fatalf("A12-029 reason %q is missing", reason)
		}
	}
}

func expectedNonExecutionWorkflow(reason DenialCode, mode Mode) (FindingWorkflowState, []string) {
	if mode == ModeDisabled || !IsActionableNonExecutionReason(reason) {
		return FindingWorkflowManualRemediationRequired, []string{}
	}
	switch reason {
	case ReasonApprovalRequired:
		if mode == ModeApproval || mode == ModeAuto {
			return FindingWorkflowApprovalPending, []string{}
		}
		return FindingWorkflowManualRemediationRequired, []string{"acknowledge", "start_remediation", "dismiss"}
	case ReasonApprovalExpired:
		return FindingWorkflowManualRemediationRequired, []string{"acknowledge", "start_remediation", "dismiss"}
	case ReasonApprovalRejected:
		return FindingWorkflowManualRemediationRequired, []string{"acknowledge", "start_remediation", "dismiss"}
	case ReasonVerificationFailed:
		return FindingWorkflowVerificationPending, []string{"start_remediation", "dismiss", "resolve"}
	default:
		return FindingWorkflowManualRemediationRequired, []string{"acknowledge", "start_remediation", "dismiss"}
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
