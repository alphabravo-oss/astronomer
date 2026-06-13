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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/compliance"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
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

// SetTap wires the bus-tap cache invalidator. Optional.
func (h *WebhookHandler) SetTap(t WebhookTapInvalidator) { h.tap = t }

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
func (h *WebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	rows, err := h.queries.ListWebhookSubscriptions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to list webhook subscriptions")
		return
	}
	items := make([]subscriptionResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSubscriptionResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// Create handles POST /api/v1/admin/webhooks/.
func (h *WebhookHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	// Apply defaults for required fields.
	if req.Name == nil {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "name is required")
		return
	}
	if req.URL == nil {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "url is required")
		return
	}
	if req.Secret == nil || strings.TrimSpace(*req.Secret) == "" || *req.Secret == SecretSentinel {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "secret is required on create")
		return
	}
	merged, vErr := h.mergeForCreate(req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", vErr)
		return
	}
	// Uniqueness check (the unique index would catch this too, but a
	// pre-flight returns a friendlier error than the raw constraint
	// violation).
	if existing, err := h.queries.GetWebhookSubscriptionByName(r.Context(), merged.Name); err == nil && existing.ID != uuid.Nil {
		RespondRequestError(w, r, http.StatusConflict, "name_taken", "A webhook subscription with this name already exists")
		return
	}

	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Encryptor is not configured; cannot store webhook secret")
		return
	}
	secretEnc, err := h.encryptor.Encrypt(*req.Secret)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt webhook secret")
		return
	}

	filtersJSON, _ := json.Marshal(merged.EventFilters)
	headersJSON, _ := json.Marshal(merged.ExtraHeaders)

	saved, err := h.queries.CreateWebhookSubscription(r.Context(), sqlc.CreateWebhookSubscriptionParams{
		Name:            merged.Name,
		Url:             merged.URL,
		SecretEncrypted: secretEnc,
		EventFilters:    filtersJSON,
		PayloadTemplate: merged.PayloadTemplate,
		ExtraHeaders:    headersJSON,
		Enabled:         merged.Enabled,
		MaxRetries:      int32(merged.MaxRetries),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
		CreatedBy:       currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to create webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	recordAudit(r, h.audit, "admin.webhook.created", "webhook_subscription", saved.ID.String(), saved.Name, map[string]any{
		"url":           saved.Url,
		"event_filters": merged.EventFilters,
		"enabled":       saved.Enabled,
		"max_retries":   saved.MaxRetries,
	})
	RespondJSON(w, http.StatusCreated, toSubscriptionResponse(saved))
}

// Get handles GET /api/v1/admin/webhooks/{id}/.
func (h *WebhookHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	row, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read webhook subscription")
		return
	}
	RespondJSON(w, http.StatusOK, toSubscriptionResponse(row))
}

// Update handles PUT /api/v1/admin/webhooks/{id}/.
func (h *WebhookHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	existing, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read webhook subscription")
		return
	}
	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	merged, vErr := h.mergeForUpdate(existing, req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", vErr)
		return
	}
	// Secret handling: SecretSentinel means "keep existing". Any other
	// non-empty value is re-encrypted.
	encryptedSecret := existing.SecretEncrypted
	if req.Secret != nil && *req.Secret != SecretSentinel {
		if strings.TrimSpace(*req.Secret) == "" {
			RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "secret cannot be blanked; supply a new value or omit the field")
			return
		}
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, "not_configured", "Encryptor unavailable")
			return
		}
		enc, encErr := h.encryptor.Encrypt(*req.Secret)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, "encrypt_error", "Failed to encrypt secret")
			return
		}
		encryptedSecret = enc
	}
	filtersJSON, _ := json.Marshal(merged.EventFilters)
	headersJSON, _ := json.Marshal(merged.ExtraHeaders)

	saved, err := h.queries.UpdateWebhookSubscription(r.Context(), sqlc.UpdateWebhookSubscriptionParams{
		ID:              id,
		Name:            merged.Name,
		Url:             merged.URL,
		SecretEncrypted: encryptedSecret,
		EventFilters:    filtersJSON,
		PayloadTemplate: merged.PayloadTemplate,
		ExtraHeaders:    headersJSON,
		Enabled:         merged.Enabled,
		MaxRetries:      int32(merged.MaxRetries),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to update webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	recordAudit(r, h.audit, "admin.webhook.updated", "webhook_subscription", saved.ID.String(), saved.Name, map[string]any{
		"url":           saved.Url,
		"event_filters": merged.EventFilters,
		"enabled":       saved.Enabled,
	})
	RespondJSON(w, http.StatusOK, toSubscriptionResponse(saved))
}

// Delete handles DELETE /api/v1/admin/webhooks/{id}/. CASCADE on
// webhook_deliveries means the delivery history is wiped automatically.
func (h *WebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	existing, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read webhook subscription")
		return
	}
	// T6.064 — refuse delete when the subscription is named in the
	// active compliance baseline's required_webhooks. Operators must
	// either revert the baseline or detach the requirement first.
	if slug, required := activeBaselineRequiresWebhook(r.Context(), h.queries, existing.Name); required {
		RespondRequestError(w, r, http.StatusConflict, "baseline_required",
			fmt.Sprintf("Webhook %q is required by the active compliance baseline %q.", existing.Name, slug))
		return
	}
	if err := h.queries.DeleteWebhookSubscription(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to delete webhook subscription")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	recordAudit(r, h.audit, "admin.webhook.deleted", "webhook_subscription", id.String(), existing.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/admin/webhooks/{id}/test/. Enqueues a
// synthetic event tied to this subscription so it travels the same
// pipeline a real event would — the dispatcher picks it up on its
// next tick.
func (h *WebhookHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	sub, err := h.queries.GetWebhookSubscription(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Webhook subscription not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read webhook subscription")
		return
	}
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]any{
		"event_name": "webhook.test_ping",
		"event_id":   uuid.New().String(),
		"timestamp":  now,
		"detail": map[string]any{
			"message":      "synthetic test ping from astronomer admin",
			"triggered_by": callerUsername(r),
		},
	})
	row, err := h.queries.InsertWebhookDelivery(r.Context(), sqlc.InsertWebhookDeliveryParams{
		SubscriptionID: sub.ID,
		EventName:      "webhook.test_ping",
		EventID:        uuid.New().String(),
		Payload:        payload,
		PayloadSize:    int32(len(payload)),
		Status:         "queued",
		NextAttemptAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to enqueue test delivery")
		return
	}
	RespondJSONUnwrapped(w, http.StatusAccepted, map[string]any{
		"delivery_id":     row.ID.String(),
		"subscription_id": sub.ID.String(),
		"queued_at":       now.Format(time.RFC3339),
		"message":         "Test ping queued. The dispatcher will pick it up on the next tick (within 15s).",
	})
}

// deliveryResponse is one row in the deliveries audit view.
type deliveryResponse struct {
	ID             string  `json:"id"`
	EventName      string  `json:"event_name"`
	EventID        string  `json:"event_id"`
	Status         string  `json:"status"`
	Attempts       int     `json:"attempts"`
	PayloadSize    int     `json:"payload_size"`
	ResponseStatus int     `json:"response_status"`
	ResponseBody   string  `json:"response_body"`
	LastError      string  `json:"last_error"`
	DeliveredAt    *string `json:"delivered_at"`
	NextAttemptAt  *string `json:"next_attempt_at"`
	CreatedAt      string  `json:"created_at"`
}

// Deliveries handles GET /api/v1/admin/webhooks/{id}/deliveries/.
func (h *WebhookHandler) Deliveries(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := h.queries.ListWebhookDeliveriesBySubscription(r.Context(), sqlc.ListWebhookDeliveriesBySubscriptionParams{
		SubscriptionID: id,
		Limit:          int32(limit),
		Offset:         int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read deliveries")
		return
	}
	total, _ := h.queries.CountWebhookDeliveriesBySubscription(r.Context(), id)
	items := make([]deliveryResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDeliveryResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// RetryDelivery handles POST /api/v1/admin/webhooks/{id}/deliveries/{delivery_id}/retry/.
func (h *WebhookHandler) RetryDelivery(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	subID, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid subscription id")
		return
	}
	delID, ok := parseUUIDParam(r, "delivery_id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_id", "Invalid delivery id")
		return
	}
	row, err := h.queries.GetWebhookDelivery(r.Context(), delID)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Delivery not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "read_error", "Failed to read delivery")
		return
	}
	if row.SubscriptionID != subID {
		// Refuse cross-subscription retry — keeps the URL contract clean
		// (the {id} in the path is load-bearing for the audit + RBAC
		// view).
		RespondRequestError(w, r, http.StatusNotFound, "not_found", "Delivery does not belong to this subscription")
		return
	}
	if err := h.queries.RetryWebhookDelivery(r.Context(), sqlc.RetryWebhookDeliveryParams{
		ID:            delID,
		NextAttemptAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "write_error", "Failed to mark delivery for retry")
		return
	}
	RespondJSONUnwrapped(w, http.StatusAccepted, map[string]any{
		"delivery_id": delID.String(),
		"message":     "Delivery re-queued. The dispatcher will pick it up on the next tick (within 15s).",
	})
}

// mergedSettings is the validated, all-pointers-resolved view used by
// both the create + update paths.
type mergedSettings struct {
	Name            string
	URL             string
	EventFilters    []string
	PayloadTemplate string
	ExtraHeaders    map[string]string
	Enabled         bool
	MaxRetries      int
	TimeoutSeconds  int
}

func (h *WebhookHandler) mergeForCreate(req subscriptionRequest) (mergedSettings, string) {
	out := mergedSettings{
		Name:            "",
		URL:             "",
		EventFilters:    []string{},
		PayloadTemplate: "",
		ExtraHeaders:    map[string]string{},
		Enabled:         true,
		MaxRetries:      5,
		TimeoutSeconds:  10,
	}
	if req.Name != nil {
		out.Name = strings.TrimSpace(*req.Name)
	}
	if req.URL != nil {
		out.URL = strings.TrimSpace(*req.URL)
	}
	if req.EventFilters != nil {
		out.EventFilters = *req.EventFilters
	}
	if req.PayloadTemplate != nil {
		out.PayloadTemplate = *req.PayloadTemplate
	}
	if req.ExtraHeaders != nil {
		out.ExtraHeaders = *req.ExtraHeaders
	}
	if req.Enabled != nil {
		out.Enabled = *req.Enabled
	}
	if req.MaxRetries != nil {
		out.MaxRetries = *req.MaxRetries
	}
	if req.TimeoutSeconds != nil {
		out.TimeoutSeconds = *req.TimeoutSeconds
	}
	return out, validateMerged(out)
}

func (h *WebhookHandler) mergeForUpdate(existing sqlc.WebhookSubscription, req subscriptionRequest) (mergedSettings, string) {
	out := mergedSettings{
		Name:            existing.Name,
		URL:             existing.Url,
		PayloadTemplate: existing.PayloadTemplate,
		Enabled:         existing.Enabled,
		MaxRetries:      int(existing.MaxRetries),
		TimeoutSeconds:  int(existing.TimeoutSeconds),
	}
	// Decode existing JSONB fields into the merged shape.
	out.EventFilters = []string{}
	if len(existing.EventFilters) > 0 {
		_ = json.Unmarshal(existing.EventFilters, &out.EventFilters)
	}
	out.ExtraHeaders = map[string]string{}
	if len(existing.ExtraHeaders) > 0 {
		_ = json.Unmarshal(existing.ExtraHeaders, &out.ExtraHeaders)
	}
	// Overlay the request.
	if req.Name != nil {
		out.Name = strings.TrimSpace(*req.Name)
	}
	if req.URL != nil {
		out.URL = strings.TrimSpace(*req.URL)
	}
	if req.EventFilters != nil {
		out.EventFilters = *req.EventFilters
	}
	if req.PayloadTemplate != nil {
		out.PayloadTemplate = *req.PayloadTemplate
	}
	if req.ExtraHeaders != nil {
		out.ExtraHeaders = *req.ExtraHeaders
	}
	if req.Enabled != nil {
		out.Enabled = *req.Enabled
	}
	if req.MaxRetries != nil {
		out.MaxRetries = *req.MaxRetries
	}
	if req.TimeoutSeconds != nil {
		out.TimeoutSeconds = *req.TimeoutSeconds
	}
	return out, validateMerged(out)
}

func validateMerged(s mergedSettings) string {
	if s.Name == "" {
		return "name is required"
	}
	if len(s.Name) > 128 {
		return "name must be 128 chars or fewer"
	}
	if s.URL == "" {
		return "url is required"
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "url must be a valid http(s) URL"
	}
	if u.Host == "" {
		return "url must include a host"
	}
	if s.MaxRetries < 0 || s.MaxRetries > 50 {
		return "max_retries must be 0..50"
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 300 {
		return "timeout_seconds must be 1..300"
	}
	if err := webhook.ValidateTemplate(s.PayloadTemplate); err != nil {
		return err.Error()
	}
	return ""
}

// toSubscriptionResponse renders one row into the wire shape. We
// always set secret to the sentinel — never leak the ciphertext.
func toSubscriptionResponse(row sqlc.WebhookSubscription) subscriptionResponse {
	filters := []string{}
	if len(row.EventFilters) > 0 {
		_ = json.Unmarshal(row.EventFilters, &filters)
	}
	headers := map[string]string{}
	if len(row.ExtraHeaders) > 0 {
		_ = json.Unmarshal(row.ExtraHeaders, &headers)
	}
	createdBy := ""
	if row.CreatedBy.Valid {
		createdBy = uuid.UUID(row.CreatedBy.Bytes).String()
	}
	return subscriptionResponse{
		ID:               row.ID.String(),
		Name:             row.Name,
		URL:              row.Url,
		Secret:           SecretSentinel,
		SecretConfigured: row.SecretEncrypted != "",
		EventFilters:     filters,
		PayloadTemplate:  row.PayloadTemplate,
		ExtraHeaders:     headers,
		Enabled:          row.Enabled,
		MaxRetries:       int(row.MaxRetries),
		TimeoutSeconds:   int(row.TimeoutSeconds),
		CreatedBy:        createdBy,
		CreatedAt:        row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// toDeliveryResponse renders one delivery row.
func toDeliveryResponse(row sqlc.WebhookDelivery) deliveryResponse {
	var deliveredAt, nextAttempt *string
	if row.DeliveredAt.Valid {
		s := row.DeliveredAt.Time.UTC().Format(time.RFC3339)
		deliveredAt = &s
	}
	if row.NextAttemptAt.Valid {
		s := row.NextAttemptAt.Time.UTC().Format(time.RFC3339)
		nextAttempt = &s
	}
	return deliveryResponse{
		ID:             row.ID.String(),
		EventName:      row.EventName,
		EventID:        row.EventID,
		Status:         row.Status,
		Attempts:       int(row.Attempts),
		PayloadSize:    int(row.PayloadSize),
		ResponseStatus: int(row.ResponseStatus),
		ResponseBody:   row.ResponseBody,
		LastError:      row.LastError,
		DeliveredAt:    deliveredAt,
		NextAttemptAt:  nextAttempt,
		CreatedAt:      row.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// parseUUIDParam reads chi URL param and parses it as a UUID. Returns
// (uuid.Nil, false) on a malformed input so the handler can render a
// 400.
func parseUUIDParam(r *http.Request, name string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, name)
	if raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func (h *WebhookHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}

// activeBaselineQuerier is the narrow surface shared by the
// webhook + SMTP handlers' baseline-guard check (T6.064). Defined
// here next to the only caller so a future refactor can move it to
// a dedicated package without disturbing handler imports.
type activeBaselineQuerier interface {
	GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error)
	GetComplianceBaseline(ctx context.Context, id uuid.UUID) (sqlc.ComplianceBaseline, error)
}

// activeBaselineRequiresWebhook returns (slug, true) when the active
// baseline lists the given webhook subscription name in its
// required_webhooks set. (slug, false) when the name is not
// required, or when there is no active baseline.
//
// Failures (DB error, unknown slug) degrade open — we return
// (..., false) rather than block deletes when the lookup itself is
// broken. Logging happens in the caller's audit row when applicable.
func activeBaselineRequiresWebhook(ctx context.Context, q activeBaselineQuerier, name string) (string, bool) {
	slug, spec, ok := loadActiveBaselineSpec(ctx, q)
	if !ok {
		return "", false
	}
	for _, w := range spec.RequiredWebhooks {
		if w == name {
			return slug, true
		}
	}
	return "", false
}

// loadActiveBaselineSpec resolves the active baseline application to
// its slug + populated spec. Returns (..., false) on any error so the
// caller can decide whether to fail open or closed.
func loadActiveBaselineSpec(ctx context.Context, q activeBaselineQuerier) (string, compliance.BaselineSpec, bool) {
	if q == nil {
		return "", compliance.BaselineSpec{}, false
	}
	app, err := q.GetActiveComplianceBaselineApplication(ctx)
	if err != nil {
		return "", compliance.BaselineSpec{}, false
	}
	base, err := q.GetComplianceBaseline(ctx, app.BaselineID)
	if err != nil {
		return "", compliance.BaselineSpec{}, false
	}
	b, ok := compliance.BySlug(base.Slug)
	if !ok {
		return "", compliance.BaselineSpec{}, false
	}
	return base.Slug, b.Spec, true
}
