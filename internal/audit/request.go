package audit

import (
	"net/http"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type HTTPRequestEvent struct {
	Request         *http.Request
	Source          string
	CorrelationID   string
	UserID          pgtype.UUID
	ActorAuthMethod string
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceName    string
	StatusCode      int
	DurationMs      int64
	RequestID       string
	IPAddress       *netip.Addr
	Detail          map[string]any
	Before          any
	After           any
	Tags            map[string]string
}

func NewHTTPRequestEvent(input HTTPRequestEvent) Event {
	event := Event{
		Source:          input.Source,
		CorrelationID:   input.CorrelationID,
		UserID:          input.UserID,
		ActorAuthMethod: input.ActorAuthMethod,
		Action:          input.Action,
		ResourceType:    input.ResourceType,
		ResourceID:      input.ResourceID,
		ResourceName:    input.ResourceName,
		StatusCode:      int32(input.StatusCode),
		DurationMs:      input.DurationMs,
		RequestID:       input.RequestID,
		IPAddress:       input.IPAddress,
		Detail:          input.Detail,
		Before:          input.Before,
		After:           input.After,
		Tags:            input.Tags,
	}
	if input.Request != nil {
		event.HTTPMethod = input.Request.Method
		if input.Request.URL != nil {
			event.Path = input.Request.URL.Path
		}
		event.UserAgent = input.Request.UserAgent()
	}
	return event
}

func UserIDFromUUID(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
