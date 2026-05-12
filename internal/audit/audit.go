package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Querier is the database surface needed for the synchronous fallback path
// inside Record. Implemented by *sqlc.Queries and by hand-rolled fakes in
// tests. The async batched Writer (see writer.go) uses BatchQuerier
// directly; Record only falls back to a Querier when the package-level
// async writer has not been installed.
//
// Historically named "Writer" — kept as a deprecated alias below for the
// one external consumer (worker/tasks/cluster_decommission.go).
type Querier interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// Note: callers outside the audit package previously referenced
// audit.Writer as an interface. The async writer struct (writer.go) now
// owns the name Writer; the interface lives on as Querier. The one
// external consumer (worker/tasks/cluster_decommission.go) has been
// migrated accordingly.

// Event is the call-site payload for Record. Each field is copied into the
// sqlc.CreateAuditLogV1Params struct; the Detail map is JSON-encoded into
// the JSONB column with known secret keys redacted.
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

// Record persists an audit event. The call site passes a Querier as the
// synchronous-fallback path: when the package-level async Writer is
// installed (see SetWriter in writer.go), the event is enqueued into the
// async writer's channel and Record returns without a DB round-trip; when
// no Writer is installed (tests, bootstrap), Record falls back to a direct
// CreateAuditLogV1 call on the querier — preserving the original
// pre-async behavior so test fakes that satisfy Querier keep working.
//
// The wire contract (function signature) is unchanged from the
// pre-async version. Every existing call site continues to work
// without edits.
func Record(ctx context.Context, q Querier, event Event) {
	row, ok := buildRow(event)
	if !ok {
		// buildRow always succeeds today; the bool is for future
		// validation extensions.
		return
	}

	if w := getDefaultWriter(); w != nil {
		w.Enqueue(row)
		// Logging the "audit_recorded" event remains synchronous and is
		// independent of DB persistence — operators rely on this log
		// line in non-DB observability paths (Loki, journald) so we
		// emit it whether the row was enqueued or dropped. The dropped
		// case is separately visible via the dropped_total counter and
		// a throttled warn log inside Enqueue.
		emitRecordedLog(row)
		publishToBus(event, row)
		return
	}

	// Sync fallback: callers that initialize the audit package without a
	// Writer get the original per-request insert. Keeps tests and the
	// k3d-bootstrap-time path working unchanged.
	if q == nil {
		return
	}
	if err := q.CreateAuditLogV1(ctx, row); err != nil {
		slog.Default().Warn("audit v1 log insert failed",
			"source", row.Source,
			"action", row.Action,
			"resource_type", row.ResourceType,
			"resource_id", row.ResourceID,
			"error", err,
		)
		return
	}
	emitRecordedLog(row)
	publishToBus(event, row)
}

// BusPublisher is the optional sink the audit package publishes recorded
// events to. Wired by the server (which owns the events.Bus). The contract
// is event-name = "audit." + action, with the structured detail map +
// resource identifiers preserved as the second argument. Slow or
// nil-implementations MUST NOT block the audit hot path — see the
// fan-out shape in internal/events.Bus.Publish (best-effort with drops).
type BusPublisher interface {
	Publish(eventName string, data any)
}

var (
	busPublisherMu sync.RWMutex
	busPublisher   BusPublisher
)

// SetBusPublisher installs the package-level bus publisher. Pass nil to
// clear (tests rely on this). Idempotent.
func SetBusPublisher(p BusPublisher) {
	busPublisherMu.Lock()
	defer busPublisherMu.Unlock()
	busPublisher = p
}

func publishToBus(event Event, row sqlc.CreateAuditLogV1Params) {
	busPublisherMu.RLock()
	p := busPublisher
	busPublisherMu.RUnlock()
	if p == nil || row.Action == "" {
		return
	}
	// The published event-name is "audit.<action>" so glob filters like
	// "audit.*" subscribe to every audit row, and a more targeted glob
	// like "audit.admin.webhook.*" subscribes only to webhook config
	// changes. The detail map mirrors what a downstream receiver would
	// want without having to round-trip back to the audit_log table.
	data := map[string]any{
		"action":          row.Action,
		"resource_type":   row.ResourceType,
		"resource_id":     row.ResourceID,
		"resource_name":   row.ResourceName,
		"actor_user_id":   userIDString(row.UserID),
		"actor_auth_method": row.ActorAuthMethod,
		"correlation_id":  row.CorrelationID,
		"request_id":      row.RequestID,
		"source":          row.Source,
		"http_method":     row.HTTPMethod,
		"path":            row.Path,
		"status_code":     row.StatusCode,
		"detail":          event.Detail,
	}
	p.Publish("audit."+row.Action, data)
}

// userIDString renders the pgtype.UUID actor as a printable string or
// empty when the row carries an anonymous (NULL) user. We don't want
// "00000000-0000-0000-0000-000000000000" leaking onto the wire for
// failed-login style events.
func userIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	out, err := u.Value()
	if err != nil || out == nil {
		return ""
	}
	s, ok := out.(string)
	if !ok {
		return ""
	}
	return s
}

// buildRow turns an Event into the sqlc params struct. Detail sanitization,
// before/after stamping, and default Source/CorrelationID live here so the
// async and sync paths produce byte-identical rows.
func buildRow(event Event) (sqlc.CreateAuditLogV1Params, bool) {
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

	return sqlc.CreateAuditLogV1Params{
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
	}, true
}

func emitRecordedLog(row sqlc.CreateAuditLogV1Params) {
	observability.WithCorrelationID(
		observability.WithEvent(slog.Default(), "audit_recorded"),
		row.CorrelationID,
	).Info("audit event recorded",
		"source", row.Source,
		"action", row.Action,
		"resource_type", row.ResourceType,
		"resource_id", row.ResourceID,
	)
}
