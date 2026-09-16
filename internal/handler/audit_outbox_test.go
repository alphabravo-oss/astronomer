package handler

import "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"

// auditLogParamsFromOutbox keeps legacy audit assertions focused on the
// externally visible event envelope while exercising the durable outbox path.
func auditLogParamsFromOutbox(arg sqlc.UpsertAuditOutboxParams) sqlc.CreateAuditLogV1Params {
	return sqlc.CreateAuditLogV1Params{
		Source:          arg.Source,
		CorrelationID:   arg.CorrelationID,
		UserID:          arg.UserID,
		ActorAuthMethod: arg.ActorAuthMethod,
		Action:          arg.Action,
		ResourceType:    arg.ResourceType,
		ResourceID:      arg.ResourceID,
		ResourceName:    arg.ResourceName,
		HTTPMethod:      arg.HttpMethod,
		Path:            arg.Path,
		StatusCode:      arg.StatusCode,
		DurationMs:      arg.DurationMs,
		RequestID:       arg.RequestID,
		IpAddress:       arg.IpAddress,
		UserAgent:       arg.UserAgent,
		Detail:          arg.Detail,
		ActionClass:     arg.ActionClass,
	}
}
