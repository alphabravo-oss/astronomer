package charlie

import (
	"context"
	"fmt"
	"strings"

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
	_ = a.record(ctx, event.Action, "charlie_session", event.SessionID, event.ActorID, event.OutcomeCode, map[string]any{"visibility": event.Visibility, "resource_count": event.ResourceCount})
}

func (a *DBLifecycleAuditor) RecordCharlieFindingLifecycle(ctx context.Context, event FindingLifecycleAudit) {
	_ = a.record(ctx, event.Action, "charlie_finding", event.FindingID, event.ActorID, event.OutcomeCode, map[string]any{"resource_count": event.Resources})
}

func (a *DBLifecycleAuditor) RecordCharlieApprovalLifecycle(ctx context.Context, event ApprovalLifecycleAudit) error {
	resourceID := event.SessionID
	action := ""
	switch event.Decision {
	case "approve":
		action = "charlie.approval.approved"
	case "reject":
		action = "charlie.approval.rejected"
	default:
		return fmt.Errorf("Charlie approval audit decision is invalid")
	}
	// Charlie identifiers are opaque, so durable audit records use a bounded
	// digest rather than copying the central approval or action identifier.
	detail := map[string]any{
		"approval_digest": digestBytes([]byte(event.ApprovalID)),
		"action_digest":   digestBytes([]byte(event.ActionID)),
		"manifest_digest": event.ManifestDigest,
		"capability":      event.Capability,
		"decision":        event.Decision,
	}
	return a.record(ctx, action, "charlie_approval", resourceID, event.ActorID, event.OutcomeCode, detail)
}

func (a *DBLifecycleAuditor) RecordCharlieAuthorityMutation(ctx context.Context, event AuthorityMutationAudit) error {
	detail := make(map[string]any, len(event.Fields)+1)
	for key, value := range event.Fields {
		detail[key] = value
	}
	detail["outcome_code"] = event.OutcomeCode
	return a.recordResource(ctx, event.Action, event.ResourceType, event.ResourceID, event.ActorID, detail)
}

func (a *DBLifecycleAuditor) record(ctx context.Context, action, resourceType string, resourceID, actorID uuid.UUID, outcome string, detail map[string]any) error {
	detail["outcome_code"] = outcome
	return a.recordResource(ctx, action, resourceType, nullableAuditID(resourceID), actorID, detail)
}

func (a *DBLifecycleAuditor) recordResource(ctx context.Context, action, resourceType, resourceID string, actorID uuid.UUID, detail map[string]any) error {
	if a == nil || a.writer == nil {
		LogOperationalFailure(ctx, nil, "charlie.lifecycle_audit_persist_failed", "")
		return fmt.Errorf("Charlie lifecycle audit persistence is unavailable")
	}
	encoded, err := EncodeCharlieAuditDetail(action, resourceType, detail)
	if err != nil {
		LogOperationalFailure(ctx, nil, "charlie.lifecycle_audit_encode_failed", "")
		return fmt.Errorf("Charlie lifecycle audit encoding failed")
	}
	actor := pgtype.UUID{}
	if actorID != uuid.Nil {
		actor = pgtype.UUID{Bytes: actorID, Valid: true}
	}
	class := "read"
	if strings.HasPrefix(action, "admin.charlie.") || strings.HasPrefix(action, "charlie.feature.") || strings.HasPrefix(action, "charlie.delegation.") || strings.HasPrefix(action, "charlie.mode.") || strings.HasPrefix(action, "charlie.connection.") || strings.HasPrefix(action, "charlie.trigger.") ||
		action == "charlie.session.created" ||
		action == "charlie.session.message_accepted" || action == "charlie.session.aborted" || resourceType == "charlie_approval" || resourceType == "charlie_finding" && action != "charlie.finding.list" && action != "charlie.finding.read" {
		class = "mutation"
	}
	if err := a.writer.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source: "service", UserID: actor, ActorAuthMethod: "charlie_product_authority",
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		StatusCode: 200, Detail: encoded, ActionClass: class,
	}); err != nil {
		// The lifecycle API is intentionally fire-and-observe because several
		// records describe outcomes that have already happened. Never hide a
		// persistence failure, and never log the event payload or identifiers.
		LogOperationalFailure(ctx, nil, "charlie.lifecycle_audit_persist_failed", "")
		return fmt.Errorf("Charlie lifecycle audit persistence failed")
	}
	return nil
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
