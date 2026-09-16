package handler

import (
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- Event Endpoints ---

// ListEvents handles GET /api/v1/alerting/events/.
func (h *AlertingHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	// Filters are pushed into SQL so pagination totals are correct across
	// pages (previously status/severity/cluster were applied in-memory to a
	// single page, so status=firing returned 0 while firing events existed
	// on later pages).
	var status pgtype.Text
	if v := r.URL.Query().Get("status"); v != "" {
		status = pgtype.Text{String: v, Valid: true}
	}
	var severity pgtype.Text
	if v := r.URL.Query().Get("severity"); v != "" {
		severity = pgtype.Text{String: v, Valid: true}
	}
	var clusterID pgtype.UUID
	if v := r.URL.Query().Get("clusterId"); v != "" {
		parsed, parseErr := uuid.Parse(v)
		if parseErr != nil {
			// An unparseable cluster filter matches nothing, mirroring the
			// old in-memory string compare against a UUID column.
			paging.Write(w, []map[string]any{}, paging.Exact(0, int(limit), int(offset), 0))
			return
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	events, err := h.queries.ListAlertEventsFiltered(r.Context(), sqlc.ListAlertEventsFilteredParams{
		Status:    status,
		Severity:  severity,
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list alert events")
		return
	}

	items := alertEventResponsesBatched(r.Context(), h.queries, events)
	total, _ := h.queries.CountAlertEventsFiltered(r.Context(), sqlc.CountAlertEventsFilteredParams{
		Status:    status,
		Severity:  severity,
		ClusterID: clusterID,
	})
	paging.Write(w, items, paging.Exact(total, int(limit), int(offset), len(items)))
}

// GetEvent handles GET /api/v1/alerting/events/{id}/.
func (h *AlertingHandler) GetEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}

	event, err := h.queries.GetAlertEventByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}

	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}

// AcknowledgeEvent handles POST /api/v1/alerting/events/{id}/acknowledge/.
func (h *AlertingHandler) AcknowledgeEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}
	if _, err := h.queries.GetAlertEventByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}
	params := sqlc.AcknowledgeAlertEventParams{
		ID:               id,
		AcknowledgedByID: currentUserUUID(r),
	}
	event, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertEvent, error) {
			if mutationErr := q.AcknowledgeAlertEvent(r.Context(), params); mutationErr != nil {
				return sqlc.AlertEvent{}, mutationErr
			}
			return q.GetAlertEventByID(r.Context(), id)
		},
		func(sqlc.AlertEvent) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.event.acknowledge", resourceType: "alert_event",
				resourceID: id.String(), status: http.StatusOK,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to acknowledge alert event")
		return
	}
	h.publishAlertingChanged("event", nullableUUIDString(event.ClusterID), id)
	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}

// ResolveEvent handles POST /api/v1/alerting/events/{id}/resolve/.
func (h *AlertingHandler) ResolveEvent(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid event ID")
		return
	}
	if _, err := h.queries.GetAlertEventByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Alert event not found")
		return
	}
	params := sqlc.UpdateAlertEventStatusParams{
		ID:     id,
		Status: "resolved",
	}
	event, err := executeMutation(r, h.runTx,
		func(q AlertingMutationTx) (sqlc.AlertEvent, error) {
			if mutationErr := q.UpdateAlertEventStatus(r.Context(), params); mutationErr != nil {
				return sqlc.AlertEvent{}, mutationErr
			}
			return q.GetAlertEventByID(r.Context(), id)
		},
		func(sqlc.AlertEvent) mutationAuditEvent {
			return mutationAuditEvent{
				action: "alert.event.resolve", resourceType: "alert_event",
				resourceID: id.String(), status: http.StatusOK,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to resolve alert event")
		return
	}
	h.publishAlertingChanged("event", nullableUUIDString(event.ClusterID), id)
	RespondJSON(w, http.StatusOK, h.alertEventResponse(r.Context(), event))
}
