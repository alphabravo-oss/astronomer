package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/webhook"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

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
