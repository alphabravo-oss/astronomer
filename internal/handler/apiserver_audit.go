package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// apiserverAuditStore is the narrow DB surface the apiserver-audit
// ingest/read endpoints need. Kept as an interface so tests can supply a
// fake without a live Postgres.
type apiserverAuditStore interface {
	InsertApiserverAuditEvent(ctx context.Context, arg sqlc.InsertApiserverAuditEventParams) error
	ListApiserverAuditEventsByCluster(ctx context.Context, arg sqlc.ListApiserverAuditEventsByClusterParams) ([]sqlc.ApiserverAuditEvent, error)
	CountApiserverAuditEventsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
}

// ApiserverAuditHandler ingests kube-apiserver audit events streamed by the
// per-cluster agent and exposes them for operator read-back.
//
// P1 item 7/22 (smallest working slice): the ingest endpoint persists a
// batch of audit.k8s.io events and the list endpoint reads them. Agent-side
// tailing of the apiserver audit log and audit-policy wiring are NOT part of
// this slice.
type ApiserverAuditHandler struct {
	queries apiserverAuditStore
}

// NewApiserverAuditHandler constructs an ApiserverAuditHandler.
func NewApiserverAuditHandler(queries apiserverAuditStore) *ApiserverAuditHandler {
	return &ApiserverAuditHandler{queries: queries}
}

// auditEventInput is one audit.k8s.io Event as the agent forwards it. Only
// the fields we promote to indexed columns are pulled out by name; the whole
// object is preserved verbatim in `raw`.
type auditEventInput struct {
	AuditID string `json:"auditID"`
	Stage   string `json:"stage"`
	Verb    string `json:"verb"`
	User    struct {
		Username string `json:"username"`
	} `json:"user"`
	ObjectRef struct {
		Resource  string `json:"resource"`
		Namespace string `json:"namespace"`
	} `json:"objectRef"`
	ResponseStatus struct {
		Code int32 `json:"code"`
	} `json:"responseStatus"`
	StageTimestamp string `json:"stageTimestamp"`
}

type ingestApiserverAuditRequest struct {
	Events []json.RawMessage `json:"events"`
}

// maxIngestBody bounds the request body the ingest endpoint will read, and
// maxIngestEvents caps how many events a single request may carry. Both guard
// against an unbounded agent (or attacker) exhausting memory.
const (
	maxIngestBody   = 4 << 20 // 4 MiB
	maxIngestEvents = 1000
)

type ingestApiserverAuditResponse struct {
	Accepted int `json:"accepted"`
	Skipped  int `json:"skipped"`
}

// Ingest handles POST /api/v1/clusters/{cluster_id}/apiserver-audit/.
// Body: {"events": [<audit.k8s.io Event>, ...]}. Events without an auditID
// are skipped; persistence is idempotent on (cluster_id, auditID).
func (h *ApiserverAuditHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBody)
	var req ingestApiserverAuditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if _, ok := err.(*http.MaxBytesError); ok {
			RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.ValidationError, "Request body too large")
			return
		}
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid request body")
		return
	}
	if len(req.Events) > maxIngestEvents {
		req.Events = req.Events[:maxIngestEvents]
	}

	accepted, skipped, err := h.PersistAuditEvents(r.Context(), clusterID, req.Events)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to persist audit event")
		return
	}

	RespondJSON(w, http.StatusAccepted, ingestApiserverAuditResponse{Accepted: accepted, Skipped: skipped})
}

// PersistAuditEvents maps each raw audit.k8s.io Event to its indexed columns
// and writes it, idempotent on (cluster_id, audit_id). Events without an
// auditID, or with a present-but-malformed stageTimestamp, are skipped (not
// errored). It returns the accepted/skipped counts; a non-nil error means a
// row write failed and the batch must be retried. Shared by the HTTP ingest
// handler and the tunnel receiver so both persist identically.
func (h *ApiserverAuditHandler) PersistAuditEvents(ctx context.Context, clusterID uuid.UUID, events []json.RawMessage) (accepted, skipped int, err error) {
	for _, raw := range events {
		var ev auditEventInput
		if err := json.Unmarshal(raw, &ev); err != nil || ev.AuditID == "" {
			skipped++
			continue
		}
		eventTime := time.Now().UTC()
		if ev.StageTimestamp != "" {
			t, perr := time.Parse(time.RFC3339, ev.StageTimestamp)
			if perr != nil {
				// Present-but-malformed timestamp: skip rather than substitute
				// now(), which would misrepresent when the event happened.
				skipped++
				continue
			}
			eventTime = t.UTC()
		}
		if err := h.queries.InsertApiserverAuditEvent(ctx, sqlc.InsertApiserverAuditEventParams{
			ClusterID:  clusterID,
			AuditID:    ev.AuditID,
			Stage:      ev.Stage,
			Verb:       ev.Verb,
			Username:   ev.User.Username,
			Resource:   ev.ObjectRef.Resource,
			Namespace:  ev.ObjectRef.Namespace,
			StatusCode: ev.ResponseStatus.Code,
			EventTime:  eventTime,
			Raw:        raw,
		}); err != nil {
			return accepted, skipped, err
		}
		accepted++
	}
	return accepted, skipped, nil
}

// List handles GET /api/v1/clusters/{cluster_id}/apiserver-audit/.
func (h *ApiserverAuditHandler) List(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	limit := int32(queryInt(r, "limit", 50))
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	offset := int32(queryInt(r, "offset", 0))
	if offset < 0 {
		offset = 0
	}

	events, err := h.queries.ListApiserverAuditEventsByCluster(r.Context(), sqlc.ListApiserverAuditEventsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list apiserver audit events")
		return
	}

	total, err := h.queries.CountApiserverAuditEventsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count apiserver audit events")
		return
	}

	RespondPaginated(w, r, events, total)
}
