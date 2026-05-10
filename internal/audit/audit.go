package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type Writer interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

type Event struct {
	Source          string
	CorrelationID   string
	UserID          pgtype.UUID
	ActorAuthMethod string
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceName    string
	HTTPMethod      string
	Path            string
	StatusCode      int32
	DurationMs      int64
	RequestID       string
	IPAddress       *netip.Addr
	UserAgent       string
	Detail          map[string]any
	Before          any
	After           any
	Tags            map[string]string
}

var secretKeys = map[string]struct{}{
	"password":              {},
	"current_password":      {},
	"new_password":          {},
	"registry_password":     {},
	"token":                 {},
	"auth_token":            {},
	"auth_token_encrypted":  {},
	"client_secret":         {},
	"secret":                {},
	"private_key":           {},
	"ca_bundle":             {},
	"kubeconfig":            {},
	"bind_password":         {},
	"bindpw":                {},
	"access_key":            {},
	"secret_key":            {},
	"aws_secret_access_key": {},
}

func SanitizeDetail(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		lk := strings.ToLower(k)
		if _, redact := secretKeys[lk]; redact {
			out[k] = "[redacted]"
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return SanitizeDetail(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = sanitizeValue(value[i])
		}
		return out
	default:
		return v
	}
}

func Record(ctx context.Context, writer Writer, event Event) {
	if writer == nil {
		return
	}

	raw := json.RawMessage(`{}`)
	payload := map[string]any{}
	for k, v := range SanitizeDetail(event.Detail) {
		payload[k] = v
	}
	if event.Before != nil {
		payload["before"] = sanitizeValue(event.Before)
	}
	if event.After != nil {
		payload["after"] = sanitizeValue(event.After)
	}
	if len(event.Tags) > 0 {
		payload["tags"] = event.Tags
	}
	if len(payload) > 0 {
		buf, err := json.Marshal(payload)
		if err == nil {
			raw = buf
		}
	}

	if event.Source == "" {
		event.Source = "service"
	}
	if event.CorrelationID == "" {
		event.CorrelationID = event.RequestID
	}

	if err := writer.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source:          event.Source,
		CorrelationID:   event.CorrelationID,
		UserID:          event.UserID,
		ActorAuthMethod: event.ActorAuthMethod,
		Action:          event.Action,
		ResourceType:    event.ResourceType,
		ResourceID:      event.ResourceID,
		ResourceName:    event.ResourceName,
		HTTPMethod:      event.HTTPMethod,
		Path:            event.Path,
		StatusCode:      event.StatusCode,
		DurationMs:      event.DurationMs,
		RequestID:       event.RequestID,
		IpAddress:       event.IPAddress,
		UserAgent:       event.UserAgent,
		Detail:          raw,
	}); err != nil {
		slog.Default().Warn("audit v1 log insert failed",
			"source", event.Source,
			"action", event.Action,
			"resource_type", event.ResourceType,
			"resource_id", event.ResourceID,
			"error", err,
		)
		return
	}

	observability.WithCorrelationID(
		observability.WithEvent(slog.Default(), "audit_recorded"),
		event.CorrelationID,
	).Info("audit event recorded",
		"source", event.Source,
		"action", event.Action,
		"resource_type", event.ResourceType,
		"resource_id", event.ResourceID,
	)
}
