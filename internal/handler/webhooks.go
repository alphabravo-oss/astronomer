// Package handler — admin CRUD for webhook_subscriptions + the
// deliveries audit view (migration 048).
//
// Route summary (all superuser-gated):
//
//	GET    /api/v1/admin/webhooks/                          — list
//	POST   /api/v1/admin/webhooks/                          — create
//	GET    /api/v1/admin/webhooks/{id}/                     — get
//	PUT    /api/v1/admin/webhooks/{id}/                     — update
//	DELETE /api/v1/admin/webhooks/{id}/                     — delete
//	POST   /api/v1/admin/webhooks/{id}/test/                — send synthetic event
//	GET    /api/v1/admin/webhooks/{id}/deliveries/          — paginated history
//	POST   /api/v1/admin/webhooks/{id}/deliveries/{id}/retry/ — force re-dispatch

package handler

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

// SecretSentinel is the placeholder returned in GET responses instead
// of the raw ciphertext. The dashboard PUT path echoes this value back
// when the admin didn't change the secret, and the handler recognises
// it as "keep existing" so a fresh PUT doesn't accidentally blank it.
// Same pattern as PasswordSentinelEncrypted in smtp.go.
const SecretSentinel = "<encrypted>"

// WebhookQuerier is the database surface WebhookHandler needs.
// *sqlc.Queries satisfies it directly; tests pass a narrow fake.
type WebhookQuerier interface {
	ListWebhookSubscriptions(ctx context.Context) ([]sqlc.WebhookSubscription, error)
	GetWebhookSubscription(ctx context.Context, id uuid.UUID) (sqlc.WebhookSubscription, error)
	GetWebhookSubscriptionByName(ctx context.Context, name string) (sqlc.WebhookSubscription, error)
	CreateWebhookSubscription(ctx context.Context, arg sqlc.CreateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error)
	UpdateWebhookSubscription(ctx context.Context, arg sqlc.UpdateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error)
	DeleteWebhookSubscription(ctx context.Context, id uuid.UUID) error
	GetWebhookDelivery(ctx context.Context, id uuid.UUID) (sqlc.WebhookDelivery, error)
	ListWebhookDeliveriesBySubscription(ctx context.Context, arg sqlc.ListWebhookDeliveriesBySubscriptionParams) ([]sqlc.WebhookDelivery, error)
	CountWebhookDeliveriesBySubscription(ctx context.Context, subscriptionID uuid.UUID) (int64, error)
	InsertWebhookDelivery(ctx context.Context, arg sqlc.InsertWebhookDeliveryParams) (sqlc.WebhookDelivery, error)
	RetryWebhookDelivery(ctx context.Context, arg sqlc.RetryWebhookDeliveryParams) error
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// T6.064 — active-baseline lookup. Used to refuse delete when the
	// subscription is listed in `required_webhooks` of the active
	// compliance baseline. *sqlc.Queries satisfies this natively.
	GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error)
	GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error)
}

type WebhookMutationTx interface {
	audit.OutboxQuerier
	CreateWebhookSubscription(context.Context, sqlc.CreateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error)
	UpdateWebhookSubscription(context.Context, sqlc.UpdateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error)
	DeleteWebhookSubscription(context.Context, uuid.UUID) error
	InsertWebhookDelivery(context.Context, sqlc.InsertWebhookDeliveryParams) (sqlc.WebhookDelivery, error)
	RetryWebhookDelivery(context.Context, sqlc.RetryWebhookDeliveryParams) error
}

type webhookRunTxFunc func(context.Context, func(WebhookMutationTx) error) error

// WebhookTapInvalidator is the cache hook on the bus tap. Wired by the
// server so a CRUD operation reflects on the next event. Optional.
type WebhookTapInvalidator interface {
	Invalidate()
}

// WebhookHandler owns /api/v1/admin/webhooks/* and the deliveries
// sub-routes. Superuser-gated inside the handler so non-admins get a
// clean 403.
type WebhookHandler struct {
	queries   WebhookQuerier
	encryptor *auth.Encryptor
	log       *slog.Logger
	audit     AuthAuditWriter
	tap       WebhookTapInvalidator
	// override, when set and returning true, lets the caller delete a
	// webhook that the active compliance baseline marks required
	// (T6.064 deletion guard). nil → guard always enforced.
	override BaselineOverrideChecker
	runTx    webhookRunTxFunc
}

// NewWebhookHandler builds a usable handler.
func NewWebhookHandler(queries WebhookQuerier, encryptor *auth.Encryptor, log *slog.Logger) *WebhookHandler {
	if log == nil {
		log = slog.Default()
	}
	return &WebhookHandler{
		queries:   queries,
		encryptor: encryptor,
		log:       log,
	}
}

// SetAuditWriter wires the audit log writer. Required for admin actions
// to leave an audit_log row.
func (h *WebhookHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

func (h *WebhookHandler) SetRunTx(runTx webhookRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *WebhookHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetTap wires the bus-tap cache invalidator. Optional.
func (h *WebhookHandler) SetTap(t WebhookTapInvalidator) { h.tap = t }

// SetBaselineOverrideChecker wires the RBAC override predicate used by
// the compliance deletion guard. Optional — when unset, a webhook the
// active baseline requires can never be deleted.
func (h *WebhookHandler) SetBaselineOverrideChecker(c BaselineOverrideChecker) { h.override = c }

// subscriptionResponse mirrors the JSON shape returned by every read.
// secret is always the sentinel — we NEVER leak the encrypted column
// over the wire.
type subscriptionResponse struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	URL              string            `json:"url"`
	Secret           string            `json:"secret"` // sentinel
	SecretConfigured bool              `json:"secret_configured"`
	EventFilters     []string          `json:"event_filters"`
	PayloadTemplate  string            `json:"payload_template"`
	ExtraHeaders     map[string]string `json:"extra_headers"`
	Enabled          bool              `json:"enabled"`
	MaxRetries       int               `json:"max_retries"`
	TimeoutSeconds   int               `json:"timeout_seconds"`
	CreatedBy        string            `json:"created_by,omitempty"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

// subscriptionRequest is the POST/PUT body. Every field is a pointer so
// PUT can do partial updates while POST sets explicit defaults at the
// helper layer.
// openapi:request WebhookSubscriptionRequest
type subscriptionRequest struct {
	Name            *string            `json:"name"`
	URL             *string            `json:"url"`
	Secret          *string            `json:"secret"`
	EventFilters    *[]string          `json:"event_filters"`
	PayloadTemplate *string            `json:"payload_template"`
	ExtraHeaders    *map[string]string `json:"extra_headers"`
	Enabled         *bool              `json:"enabled"`
	MaxRetries      *int               `json:"max_retries"`
	TimeoutSeconds  *int               `json:"timeout_seconds"`
}

// List handles GET /api/v1/admin/webhooks/.
