package charlie

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type acceptanceFindingStore struct {
	inputs []FindingInput
	row    DurableFinding
}

func (s *acceptanceFindingStore) UpsertBlockedFinding(_ context.Context, input FindingInput, _ FindingRecommendation, _ string) (DurableFinding, error) {
	s.inputs = append(s.inputs, input)
	return s.row, nil
}

func executeAcceptanceAction(t *testing.T, facts AuthorityInput) (ActionResult, *acceptanceFindingStore, *fakeFindingPublisher, *fakeCapabilityExecutor, *fakeLiveAuthority, *fakeReceipts, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakeLiveAuthority{facts: []AuthorityInput{facts, facts}}
	receipts := &fakeReceipts{}
	executor := &fakeCapabilityExecutor{verified: true}
	store := &acceptanceFindingStore{row: DurableFinding{ID: "finding-a", Status: "open", RepeatCount: 1, Notify: true}}
	publisher := &fakeFindingPublisher{}
	findings, err := NewFindingService(store, publisher)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewActionGuard(publicKey, authority, receipts, executor, &fakeActionAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	guard.SetFindingRecorder(findings, "installation-a")
	action := signedTestAction(t, privateKey, "astronomer.queue.retry_task", map[string]any{
		"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a",
	})
	if facts.ApprovalRequested || (facts.Mode == ModeApproval && facts.ApprovalPresent) {
		action.ApprovalID = "approval-a"
		payload, marshalErr := json.Marshal(action.signed())
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		action.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	}
	return guard.Execute(context.Background(), action), store, publisher, executor, authority, receipts, action.ActionID
}

func TestAAlert006EffectiveStateProducesOnlyThePermittedOutcome(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name          string
		mutate        func(*AuthorityInput)
		wantCode      DenialCode
		wantExecute   int
		wantFinding   int
		wantWorkflow  FindingWorkflowState
		wantDecisions int
	}{
		{name: "feature absent", mutate: func(f *AuthorityInput) { f.FeatureEnabled = false }, wantCode: DeniedFeatureDisabled},
		{name: "connection disabled", mutate: func(f *AuthorityInput) { f.ConnectionActive = false }, wantCode: DeniedConnectionInactive},
		{name: "wire disabled control only", mutate: func(f *AuthorityInput) { f.Mode = ModeDisabled }, wantCode: DeniedModeDisabled},
		{name: "read only manual guidance", mutate: func(f *AuthorityInput) { f.Mode = ModeReadOnly }, wantCode: DeniedReadOnlyWrite, wantFinding: 1, wantWorkflow: FindingWorkflowManualRemediationRequired, wantDecisions: 3},
		{name: "approval exact decision pending", mutate: func(f *AuthorityInput) { f.Mode = ModeApproval; f.ApprovalPresent = false }, wantCode: DeniedApprovalRequired, wantFinding: 1, wantWorkflow: FindingWorkflowApprovalPending},
		{name: "approval verified result", mutate: func(f *AuthorityInput) { f.Mode = ModeApproval }, wantExecute: 1},
		{name: "auto verified result", mutate: func(f *AuthorityInput) { f.Mode = ModeAuto }, wantExecute: 1},
		{name: "auto exact approval retained", mutate: func(f *AuthorityInput) { f.Mode = ModeAuto; f.ApprovalRequested = true }, wantExecute: 1},
		{name: "auto noneligible manual fallback", mutate: func(f *AuthorityInput) { f.Mode = ModeAuto; f.AutoEligible = false }, wantCode: DeniedNotAutoEligible, wantFinding: 1, wantWorkflow: FindingWorkflowManualRemediationRequired, wantDecisions: 3},
		{name: "auto nonallowlisted manual fallback", mutate: func(f *AuthorityInput) { f.Mode = ModeAuto; f.Allowlisted = false }, wantCode: DeniedNotAllowlisted, wantFinding: 1, wantWorkflow: FindingWorkflowManualRemediationRequired, wantDecisions: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := allowedWriteFacts(ModeApproval)
			test.mutate(&facts)
			result, store, publisher, executor, authority, receipts, _ := executeAcceptanceAction(t, facts)
			if result.Code != test.wantCode || executor.calls != test.wantExecute || len(store.inputs) != test.wantFinding || len(publisher.alerts) != test.wantFinding {
				t.Fatalf("outcome=%+v execute=%d findings=%d alerts=%d", result, executor.calls, len(store.inputs), len(publisher.alerts))
			}
			if test.wantExecute == 0 && (authority.commitCalls != 0 || receipts.claimCalls != 0) {
				t.Fatalf("non-executing outcome consumed authority: commits=%d claims=%d", authority.commitCalls, receipts.claimCalls)
			}
			if test.wantFinding == 0 {
				if result.Finding != nil {
					t.Fatalf("silent/automatic state exposed a finding: %+v", result.Finding)
				}
				return
			}
			input := store.inputs[0]
			reason, ok := NormalizeNonExecutionReason(input.Decision.Code)
			if !ok || publisher.alerts[0].BlockCode != string(reason) {
				t.Fatalf("finding/alert reason drift: input=%+v alert=%+v", input, publisher.alerts[0])
			}
			row := sqlc.CharlieFinding{
				Status: "open", EffectiveMode: string(input.Mode), ExecutionBlockCode: string(reason),
			}
			if reason == ReasonApprovalRequired {
				row.ApprovalID = pgtype.Text{String: "approval-a", Valid: true}
				row.ExpiresAt = pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}
			}
			workflow := FindingWorkflowFor(row, now)
			if workflow.State != test.wantWorkflow || len(workflow.Decisions) != test.wantDecisions {
				t.Fatalf("workflow=%+v", workflow)
			}
		})
	}
}

func TestAuthorityMatrixRolesTargetRBACAndCumulativeModes(t *testing.T) {
	roles := []struct {
		name       string
		canApprove bool
		automation bool
	}{
		{name: "viewer"},
		{name: "operator"},
		{name: "approver", canApprove: true},
		{name: "administrator", canApprove: true},
		{name: "automation identity", automation: true},
	}
	for _, role := range roles {
		for _, targetAllowed := range []bool{false, true} {
			for _, mode := range []Mode{ModeReadOnly, ModeApproval, ModeAuto} {
				name := fmt.Sprintf("%s/%s/target=%t", mode, role.name, targetAllowed)
				t.Run(name, func(t *testing.T) {
					facts := allowedWriteFacts(mode)
					facts.LiveAuthorized = targetAllowed
					switch mode {
					case ModeReadOnly:
						facts.ApprovalPresent = false
					case ModeApproval:
						facts.ApprovalPresent = role.canApprove
						facts.ApprovalExact = role.canApprove
					case ModeAuto:
						// Unattended execution uses only the separate narrow service
						// identity; a human role is never treated as automation.
						facts.LiveAuthorized = targetAllowed && role.automation
					}
					result, store, publisher, executor, authority, receipts, _ := executeAcceptanceAction(t, facts)
					wantExecute := mode == ModeApproval && role.canApprove && targetAllowed || mode == ModeAuto && role.automation && targetAllowed
					if wantExecute {
						if !result.Allowed || result.State != "succeeded" || !result.Verified || executor.calls != 1 || authority.commitCalls != 1 || len(store.inputs) != 0 || len(publisher.alerts) != 0 {
							t.Fatalf("eligible exact action failed: result=%+v execute=%d commits=%d findings=%d alerts=%d", result, executor.calls, authority.commitCalls, len(store.inputs), len(publisher.alerts))
						}
						return
					}
					if result.Allowed || executor.calls != 0 || authority.commitCalls != 0 || receipts.claimCalls != 0 || len(store.inputs) != 1 || len(publisher.alerts) != 1 {
						t.Fatalf("ineligible combination was dropped or executed: result=%+v execute=%d commit=%d claims=%d findings=%d alerts=%d", result, executor.calls, authority.commitCalls, receipts.claimCalls, len(store.inputs), len(publisher.alerts))
					}
				})
			}
		}
	}
}

func TestDestructiveAndConfusedDeputyCorpusDeniedAcrossModesAndRoles(t *testing.T) {
	const sentinel = "PROMPT-EVIDENCE-TOOL-ARGS-RESULT-TOKEN-SECRET-SENTINEL"
	roles := []string{"viewer", "operator", "approver", "administrator", "automation"}
	corpus := []struct {
		name       string
		capability string
		arguments  map[string]any
	}{
		{name: "shell", capability: "astronomer.shell.run", arguments: map[string]any{"command": sentinel}},
		{name: "exec", capability: "astronomer.kubernetes.exec", arguments: map[string]any{"pod": sentinel}},
		{name: "raw SQL", capability: "astronomer.database.raw_sql", arguments: map[string]any{"query": sentinel}},
		{name: "generic proxy", capability: "astronomer.http.proxy", arguments: map[string]any{"url": "https://" + sentinel}},
		{name: "Secret access", capability: "astronomer.kubernetes.secrets.get", arguments: map[string]any{"name": sentinel}},
		{name: "delete", capability: "astronomer.management.workload.delete", arguments: map[string]any{"resource_id": "resource-a"}},
		{name: "downstream transport", capability: "astronomer.downstream.cluster.proxy", arguments: map[string]any{"cluster_id": sentinel}},
		{name: "forged authority", capability: "astronomer.queue.retry_task", arguments: map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a", "authorization_ref": sentinel}},
		{name: "prompt injection", capability: "astronomer.queue.retry_task", arguments: map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a", "prompt": sentinel}},
		{name: "target substitution", capability: "astronomer.queue.retry_task", arguments: map[string]any{"resource_id": "other-resource", "task_id": "task-a", "operation_id": "action-a"}},
	}
	for _, mode := range []Mode{ModeDisabled, ModeReadOnly, ModeApproval, ModeAuto} {
		for _, role := range roles {
			for _, attack := range corpus {
				name := strings.Join([]string{string(mode), role, attack.name}, "/")
				t.Run(name, func(t *testing.T) {
					publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
					if err != nil {
						t.Fatal(err)
					}
					facts := allowedWriteFacts(mode)
					if attack.name == "target substitution" {
						facts.LiveAuthorized = false
					}
					authority := &fakeLiveAuthority{facts: []AuthorityInput{facts}}
					receipts := &fakeReceipts{}
					executor := &fakeCapabilityExecutor{}
					auditor := &fakeActionAuditor{}
					guard, err := NewActionGuard(publicKey, authority, receipts, executor, auditor)
					if err != nil {
						t.Fatal(err)
					}
					var output strings.Builder
					guard.SetLogger(slog.New(slog.NewJSONHandler(&output, nil)))
					action := signedTestAction(t, privateKey, attack.capability, attack.arguments)
					result := guard.Execute(context.Background(), action)
					replay := guard.Execute(context.Background(), action)
					if result.Allowed || replay.Allowed || executor.calls != 0 || authority.commitCalls != 0 || receipts.claimCalls != 0 || len(auditor.phases) == 0 ||
						len(auditor.results) == 0 || !isAuthorityDenialCode(auditor.results[len(auditor.results)-1].Code) || len(auditor.results[len(auditor.results)-1].Result) != 0 {
						t.Fatalf("attack reached dispatch or missed coded audit: result=%+v execute=%d commit=%d claims=%d audit=%v", result, executor.calls, authority.commitCalls, receipts.claimCalls, auditor.phases)
					}
					if strings.Contains(output.String(), sentinel) {
						t.Fatalf("operational log leaked attack content: %s", output.String())
					}
				})
			}
		}
	}
}
