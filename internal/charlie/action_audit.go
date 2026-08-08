package charlie

import (
	"context"
	"fmt"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ActionAuditor interface {
	Record(context.Context, string, ActionEnvelope, CapabilityDescriptor, ActionResult) error
}

type actionAuditQueries interface {
	CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error
}

type DBActionAuditor struct{ queries actionAuditQueries }

func NewDBActionAuditor(queries actionAuditQueries) (*DBActionAuditor, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie action auditor is unavailable")
	}
	return &DBActionAuditor{queries: queries}, nil
}

func (a *DBActionAuditor) Record(ctx context.Context, phase string, envelope ActionEnvelope, capability CapabilityDescriptor, result ActionResult) error {
	resultDigest := ""
	if len(result.Result) > 0 {
		resultDigest = digestBytes(result.Result)
	}
	action := "charlie.action." + phase
	detail, err := EncodeCharlieAuditDetail(action, "charlie_action", map[string]any{
		"phase": phase, "action_digest": digestBytes([]byte(envelope.ActionID)),
		"argument_digest": digestBytes([]byte(envelope.ArgumentDigest)), "authorization_digest": digestBytes([]byte(envelope.AuthorizationRef)),
		"capability_digest": digestBytes([]byte(envelope.Capability)), "effect": string(capability.Effect),
		"state": result.State, "denial_code": string(result.Code), "result_digest": resultDigest,
		"mode_revision": envelope.ModeRevision, "policy_revision": envelope.PolicyRevision, "fencing_epoch": envelope.FencingEpoch,
	})
	if err != nil {
		return err
	}
	actionClass := "mutation"
	if capability.Effect == EffectRead {
		actionClass = "read"
	}
	return a.queries.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source: "charlie_mcp", CorrelationID: digestBytes([]byte(envelope.ActionID)), ActorAuthMethod: "charlie_mtls_signed_action",
		Action: action, ResourceType: "charlie_action", ResourceID: digestBytes([]byte(envelope.ActionID)),
		HTTPMethod: "MCP", StatusCode: actionAuditStatus(result), Detail: detail, ActionClass: actionClass,
	})
}

func actionAuditStatus(result ActionResult) int32 {
	if result.Allowed {
		return 200
	}
	if result.Code != "" {
		return 403
	}
	return 202
}
