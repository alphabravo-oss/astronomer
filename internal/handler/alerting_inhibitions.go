package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// --- Inhibition Endpoints (P-03) ---

// ListInhibitions handles GET /api/v1/admin/alerting/inhibitions/.
func (h *AlertingHandler) ListInhibitions(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 50))
	offset := int32(queryOffset(r))

	inhibitions, err := h.queries.ListAlertInhibitions(r.Context(), sqlc.ListAlertInhibitionsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list inhibitions")
		return
	}
	items := make([]map[string]any, 0, len(inhibitions))
	for _, inhibition := range inhibitions {
		items = append(items, alertInhibitionResponse(inhibition))
	}
	total, _ := h.queries.CountAlertInhibitions(r.Context())
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
}

// GetInhibition handles GET /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) GetInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	inhibition, err := h.queries.GetAlertInhibitionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	RespondJSON(w, http.StatusOK, alertInhibitionResponse(inhibition))
}

// CreateInhibition handles POST /api/v1/admin/alerting/inhibitions/.
func (h *AlertingHandler) CreateInhibition(w http.ResponseWriter, r *http.Request) {
	var req InhibitionRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if msg := validateInhibition(req); msg != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.CreateAlertInhibitionParams{
		Name:           req.Name,
		SourceMatchers: marshalMatchers(req.SourceMatchers),
		TargetMatchers: marshalMatchers(req.TargetMatchers),
		EqualLabels:    marshalEqualLabels(req.EqualLabels),
		Enabled:        enabled,
		CreatedByID:    currentUserUUID(r),
	}
	inhibition, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertInhibition, error) {
			return q.CreateAlertInhibition(r.Context(), params)
		},
		func(row sqlc.AlertInhibition) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.inhibition.create", resourceType: "alert_inhibition",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: map[string]any{"enabled": enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create inhibition")
		return
	}
	w.Header().Set("Location", "/api/v1/admin/alerting/inhibitions/"+inhibition.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, alertInhibitionResponse(inhibition))
}

// UpdateInhibition handles PUT /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) UpdateInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	if _, err := h.queries.GetAlertInhibitionByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	var req InhibitionRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if msg := validateInhibition(req); msg != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, msg)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	params := sqlc.UpdateAlertInhibitionParams{
		ID:             id,
		Name:           req.Name,
		SourceMatchers: marshalMatchers(req.SourceMatchers),
		TargetMatchers: marshalMatchers(req.TargetMatchers),
		EqualLabels:    marshalEqualLabels(req.EqualLabels),
		Enabled:        enabled,
	}
	inhibition, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertInhibition, error) {
			return q.UpdateAlertInhibition(r.Context(), params)
		},
		func(row sqlc.AlertInhibition) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.inhibition.update", resourceType: "alert_inhibition",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK,
				detail: map[string]any{"enabled": enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update inhibition")
		return
	}
	RespondJSON(w, http.StatusOK, alertInhibitionResponse(inhibition))
}

// DeleteInhibition handles DELETE /api/v1/admin/alerting/inhibitions/{id}/.
func (h *AlertingHandler) DeleteInhibition(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid inhibition ID")
		return
	}
	match, err := h.queries.GetAlertInhibitionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Inhibition not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteAlertInhibition(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.inhibition.delete", resourceType: "alert_inhibition",
				resourceID: id.String(), resourceName: match.Name, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete inhibition")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateInhibition rejects rules that can never match usefully: a rule with
// no source and no target matcher would suppress nothing (or everything), and
// a regex matcher whose pattern does not compile would fail closed at eval
// time. Return an empty string when the rule is acceptable.
func validateInhibition(req InhibitionRequest) string {
	if len(req.SourceMatchers) == 0 {
		return "At least one source matcher is required"
	}
	if len(req.TargetMatchers) == 0 {
		return "At least one target matcher is required"
	}
	for _, m := range append(append([]InhibitionMatcher{}, req.SourceMatchers...), req.TargetMatchers...) {
		if strings.TrimSpace(m.Label) == "" {
			return "Every matcher requires a label"
		}
		if m.IsRegex {
			if _, err := regexp.Compile(m.Value); err != nil {
				return fmt.Sprintf("Invalid regex for label %q: %v", m.Label, err)
			}
		}
	}
	return ""
}

func marshalMatchers(matchers []InhibitionMatcher) json.RawMessage {
	if matchers == nil {
		matchers = []InhibitionMatcher{}
	}
	raw, err := json.Marshal(matchers)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

func marshalEqualLabels(labels []string) json.RawMessage {
	if labels == nil {
		labels = []string{}
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

func alertInhibitionResponse(inhibition sqlc.AlertInhibition) map[string]any {
	var source, target []InhibitionMatcher
	var equal []string
	_ = json.Unmarshal(inhibition.SourceMatchers, &source)
	_ = json.Unmarshal(inhibition.TargetMatchers, &target)
	_ = json.Unmarshal(inhibition.EqualLabels, &equal)
	if source == nil {
		source = []InhibitionMatcher{}
	}
	if target == nil {
		target = []InhibitionMatcher{}
	}
	if equal == nil {
		equal = []string{}
	}
	return map[string]any{
		"id":              inhibition.ID.String(),
		"name":            inhibition.Name,
		"source_matchers": source,
		"target_matchers": target,
		"equal_labels":    equal,
		"enabled":         inhibition.Enabled,
		"created_at":      inhibition.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":      inhibition.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
