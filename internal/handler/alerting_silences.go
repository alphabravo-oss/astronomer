package handler

import (
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Silence Endpoints ---

// ListSilences handles GET /api/v1/alerting/silences/.
func (h *AlertingHandler) ListSilences(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	silences, err := h.queries.ListAlertSilences(r.Context(), sqlc.ListAlertSilencesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert silences")
		return
	}

	items := make([]map[string]any, 0, len(silences))
	for _, silence := range silences {
		items = append(items, alertSilenceResponse(silence))
	}
	total, _ := h.queries.CountAlertSilences(r.Context())
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
}

// CreateSilence handles POST /api/v1/alerting/silences/.
func (h *AlertingHandler) CreateSilence(w http.ResponseWriter, r *http.Request) {
	var req CreateSilenceRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	if req.EndsAt.IsZero() {
		if req.Duration == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Silence end time is required")
			return
		}
	}

	var ruleID pgtype.UUID
	if req.RuleID == nil {
		req.RuleID = parseMatcherUUID(req.Matchers, "rule_id", "ruleId")
	}
	if req.RuleID != nil {
		ruleID = pgtype.UUID{Bytes: *req.RuleID, Valid: true}
	}

	var clusterID pgtype.UUID
	if req.ClusterID == nil {
		req.ClusterID = parseMatcherUUID(req.Matchers, "cluster_id", "clusterId")
	}
	if req.ClusterID != nil {
		clusterID = pgtype.UUID{Bytes: *req.ClusterID, Valid: true}
	}

	startsAt := req.StartsAt
	if startsAt.IsZero() {
		startsAt = time.Now()
	}
	endsAt := req.EndsAt
	if endsAt.IsZero() {
		duration, err := time.ParseDuration(req.Duration)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid silence duration")
			return
		}
		endsAt = startsAt.Add(duration)
	}

	params := sqlc.CreateAlertSilenceParams{
		RuleID:      ruleID,
		ClusterID:   clusterID,
		Reason:      req.Reason,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		CreatedByID: currentUserUUID(r),
	}
	silence, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertSilence, error) {
			return q.CreateAlertSilence(r.Context(), params)
		},
		func(row sqlc.AlertSilence) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.silence.create", resourceType: "alert_silence",
				resourceID: row.ID.String(), resourceName: req.Reason, status: http.StatusCreated,
				detail: map[string]any{
					"starts_at": startsAt.UTC().Format(time.RFC3339),
					"ends_at":   endsAt.UTC().Format(time.RFC3339),
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create alert silence")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	h.publishAlertingChanged("silence", nullableUUIDString(silence.ClusterID), silence.ID)

	w.Header().Set("Location", "/api/v1/alerting/silences/"+silence.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, alertSilenceResponse(silence))
}

// ExpireSilence handles POST /api/v1/alerting/silences/{id}/expire/.
// Currently this deletes the silence (we lack an UpdateAlertSilence query).
// The response shape preserves the original record for the UI to refresh.
func (h *AlertingHandler) ExpireSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}
	match, err := h.queries.GetAlertSilenceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	if !match.EndsAt.After(time.Now()) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AlreadyExpired, "This silence has already expired.")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteAlertSilence(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.silence.expire", resourceType: "alert_silence",
				resourceID: id.String(), resourceName: match.Reason, status: http.StatusOK,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to expire silence")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())
	h.publishAlertingChanged("silence", nullableUUIDString(match.ClusterID), id)
	expired := match
	expired.EndsAt = time.Now()
	RespondJSON(w, http.StatusOK, alertSilenceResponse(expired))
}

// DeleteSilence handles DELETE /api/v1/alerting/silences/{id}/.
func (h *AlertingHandler) DeleteSilence(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid silence ID")
		return
	}

	match, err := h.queries.GetAlertSilenceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteAlertSilence(r.Context(), id)
		},
		func(struct{}) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.silence.delete", resourceType: "alert_silence",
				resourceID: id.String(), resourceName: match.Reason, status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusNotFound, apierror.NotFound, "Alert silence not found")
		return
	}
	_ = h.syncSharedAlertingAssets(r.Context())

	h.publishAlertingChanged("silence", nullableUUIDString(match.ClusterID), id)
	w.WriteHeader(http.StatusNoContent)
}
