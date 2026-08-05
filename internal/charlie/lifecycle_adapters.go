package charlie

import (
	"context"
	"encoding/json"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type lifecycleAuditWriter interface {
	CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error
}

// DBLifecycleAuditor writes metadata-only lifecycle records. Content, evidence,
// model responses, tool arguments/results, credentials, and authorization
// references never enter this adapter.
type DBLifecycleAuditor struct{ writer lifecycleAuditWriter }

func NewDBLifecycleAuditor(writer lifecycleAuditWriter) *DBLifecycleAuditor {
	return &DBLifecycleAuditor{writer: writer}
}

func (a *DBLifecycleAuditor) RecordCharlieSessionLifecycle(ctx context.Context, event SessionLifecycleAudit) {
	a.record(ctx, event.Action, "charlie_session", event.SessionID, event.ActorID, event.OutcomeCode, map[string]any{"visibility": event.Visibility, "resource_count": event.ResourceCount})
}

func (a *DBLifecycleAuditor) RecordCharlieFindingLifecycle(ctx context.Context, event FindingLifecycleAudit) {
	a.record(ctx, event.Action, "charlie_finding", event.FindingID, event.ActorID, event.OutcomeCode, map[string]any{"resource_count": event.Resources})
}

func (a *DBLifecycleAuditor) RecordCharlieApprovalLifecycle(ctx context.Context, event ApprovalLifecycleAudit) {
	resourceID := event.SessionID
	// Charlie identifiers are opaque, so durable audit records use a bounded
	// digest rather than copying the central approval or action identifier.
	detail := map[string]any{
		"approval_digest": digestBytes([]byte(event.ApprovalID)),
		"action_digest":   digestBytes([]byte(event.ActionID)),
		"manifest_digest": event.ManifestDigest,
		"capability":      event.Capability,
		"decision":        event.Decision,
	}
	a.record(ctx, "charlie.approval."+event.Decision, "charlie_approval", resourceID, event.ActorID, event.OutcomeCode, detail)
}

func (a *DBLifecycleAuditor) record(ctx context.Context, action, resourceType string, resourceID, actorID uuid.UUID, outcome string, detail map[string]any) {
	if a == nil || a.writer == nil {
		return
	}
	detail["outcome_code"] = outcome
	encoded, err := json.Marshal(detail)
	if err != nil || len(encoded) > 2048 {
		return
	}
	actor := pgtype.UUID{}
	if actorID != uuid.Nil {
		actor = pgtype.UUID{Bytes: actorID, Valid: true}
	}
	class := "read"
	if action == "charlie.session.message_accepted" || action == "charlie.session.aborted" || resourceType == "charlie_finding" && action != "charlie.finding.list" && action != "charlie.finding.read" {
		class = "mutation"
	}
	_ = a.writer.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source: "service", UserID: actor, ActorAuthMethod: "charlie_product_authority",
		Action: action, ResourceType: resourceType, ResourceID: nullableAuditID(resourceID),
		StatusCode: 200, Detail: encoded, ActionClass: class,
	})
}

func nullableAuditID(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

type EventTriggerLifecyclePublisher struct{ bus *events.Bus }

func NewEventTriggerLifecyclePublisher(bus *events.Bus) *EventTriggerLifecyclePublisher {
	return &EventTriggerLifecyclePublisher{bus: bus}
}

func (p *EventTriggerLifecyclePublisher) PublishCharlieTriggerLifecycle(_ context.Context, eventID uuid.UUID, state, code string) {
	if p == nil || p.bus == nil {
		return
	}
	events.PublishChanged(p.bus, "charlie_investigation", "", eventID.String(), map[string]any{"state": state, "error_code": code})
}
