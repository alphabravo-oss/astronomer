package charlie

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type failingLifecycleAuditWriter struct{}

func (failingLifecycleAuditWriter) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return errors.New("database-SENTINEL")
}

type capturingLifecycleAuditWriter struct {
	row sqlc.CreateAuditLogV1Params
}

func (w *capturingLifecycleAuditWriter) CreateAuditLogV1(_ context.Context, row sqlc.CreateAuditLogV1Params) error {
	w.row = row
	return nil
}

func TestDBLifecycleAuditorLogsBoundedFailureWithoutPayload(t *testing.T) {
	var output bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })

	auditor := NewDBLifecycleAuditor(failingLifecycleAuditWriter{})
	auditor.RecordCharlieSessionLifecycle(context.Background(), SessionLifecycleAudit{
		Action: "charlie.session.read", SessionID: uuid.New(), ActorID: uuid.New(),
		Visibility: "private", OutcomeCode: "allowed", ResourceCount: 1,
	})

	logged := output.String()
	if !strings.Contains(logged, "charlie.lifecycle_audit_persist_failed") {
		t.Fatalf("audit failure code was not logged: %s", logged)
	}
	for _, secret := range []string{"database-SENTINEL"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("lifecycle audit failure log leaked payload %q: %s", secret, logged)
		}
	}
}

func TestDBLifecycleAuditorApprovalFailureIsReturnedAndBounded(t *testing.T) {
	var output bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })

	auditor := NewDBLifecycleAuditor(failingLifecycleAuditWriter{})
	err := auditor.RecordCharlieApprovalLifecycle(context.Background(), ApprovalLifecycleAudit{
		ApprovalID: "approval-SENTINEL", ActionID: "action-SENTINEL", SessionID: uuid.New(), ActorID: uuid.New(),
		Capability: "astronomer.queue.retry_task", Decision: "approve", OutcomeCode: "authorized", ManifestDigest: strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("approval audit persistence failure was hidden")
	}
	if strings.Contains(err.Error(), "database-SENTINEL") || strings.Contains(output.String(), "database-SENTINEL") || strings.Contains(output.String(), "approval-SENTINEL") {
		t.Fatalf("approval audit failure leaked storage or event content: err=%v log=%s", err, output.String())
	}
}

func TestDBLifecycleAuditorPersistsApprovalIntentAsMutation(t *testing.T) {
	writer := &capturingLifecycleAuditWriter{}
	auditor := NewDBLifecycleAuditor(writer)
	err := auditor.RecordCharlieApprovalLifecycle(context.Background(), ApprovalLifecycleAudit{
		ApprovalID: "approval-a", ActionID: "action-a", SessionID: uuid.New(), ActorID: uuid.New(),
		Capability: "astronomer.queue.retry_task", Decision: "approve", OutcomeCode: "authorized", ManifestDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("approval audit intent failed: %v", err)
	}
	if writer.row.Action != "charlie.approval.approved" || writer.row.ActionClass != "mutation" || !strings.Contains(string(writer.row.Detail), `"outcome_code":"authorized"`) {
		t.Fatalf("approval audit intent is not a mutation record: %+v detail=%s", writer.row, writer.row.Detail)
	}
}

func TestDBLifecycleAuditorPersistsOnlyCommandIdentity(t *testing.T) {
	writer := &capturingLifecycleAuditWriter{}
	NewDBLifecycleAuditor(writer).RecordCharlieSessionLifecycle(context.Background(), SessionLifecycleAudit{
		Action: "charlie.session.message_accepted", SessionID: uuid.New(), ActorID: uuid.New(),
		Visibility: "private", OutcomeCode: "accepted", ResourceCount: 1, CommandID: "health", CommandVersion: "1",
	})
	detail := string(writer.row.Detail)
	if !strings.Contains(detail, `"command_id":"health"`) || !strings.Contains(detail, `"command_version":"1"`) {
		t.Fatalf("command identity missing from audit: %s", detail)
	}
}

func TestDBLifecycleAuditorPersistsGenericAuthorityIntent(t *testing.T) {
	writer := &capturingLifecycleAuditWriter{}
	auditor := NewDBLifecycleAuditor(writer)
	fields := map[string]any{"enabled": true}
	err := auditor.RecordCharlieAuthorityMutation(context.Background(), AuthorityMutationAudit{
		Action: "charlie.feature.enabled", ResourceType: "charlie_feature", ResourceID: "feature.charlie", OutcomeCode: "authorized", Fields: fields,
	})
	if err != nil {
		t.Fatalf("authority audit intent failed: %v", err)
	}
	if writer.row.Action != "charlie.feature.enabled" || writer.row.ResourceID != "feature.charlie" || writer.row.ActionClass != "mutation" || !strings.Contains(string(writer.row.Detail), `"outcome_code":"authorized"`) {
		t.Fatalf("generic authority audit intent is invalid: %+v detail=%s", writer.row, writer.row.Detail)
	}
	if _, mutated := fields["outcome_code"]; mutated {
		t.Fatal("authority auditor mutated caller fields")
	}
}

func TestDBLifecycleAuditorClassifiesDeepAuthorityIntentsAsMutations(t *testing.T) {
	tests := []AuthorityMutationAudit{
		{Action: "charlie.session.created", ResourceType: "charlie_session", ResourceID: uuid.NewString(), OutcomeCode: "authorized", Fields: map[string]any{"visibility": "private", "resource_count": 1}},
		{Action: "charlie.trigger.dispatched", ResourceType: "charlie_trigger", ResourceID: uuid.NewString(), OutcomeCode: "authorized", Fields: map[string]any{"attempt": 1}},
	}
	for _, event := range tests {
		t.Run(event.Action, func(t *testing.T) {
			writer := &capturingLifecycleAuditWriter{}
			if err := NewDBLifecycleAuditor(writer).RecordCharlieAuthorityMutation(context.Background(), event); err != nil {
				t.Fatalf("authority audit intent failed: %v", err)
			}
			if writer.row.ActionClass != "mutation" {
				t.Fatalf("authority intent was classified as %q", writer.row.ActionClass)
			}
		})
	}
}
