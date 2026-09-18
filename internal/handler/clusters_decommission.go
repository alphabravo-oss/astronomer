package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"
)

// DecommissionPhaseStatus is one entry in the decommission status response.
// Mirrors the worker.tasks.phaseRecord shape so the frontend can render a
// per-phase progress indicator (when it eventually picks up the API).
type DecommissionPhaseStatus struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	StartedAt   string         `json:"started_at,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// DecommissionStatusResponse is the JSON body returned from
// GET /api/v1/clusters/{id}/decommission/ and POST .../decommission/ (the
// 202-Accepted enqueue path).
type DecommissionStatusResponse struct {
	DecommissionID string                    `json:"decommission_id"`
	ClusterID      string                    `json:"cluster_id"`
	ClusterName    string                    `json:"cluster_name"`
	Status         string                    `json:"status"`
	Attempts       int32                     `json:"attempts"`
	StartedAt      string                    `json:"started_at,omitempty"`
	CompletedAt    string                    `json:"completed_at,omitempty"`
	LastError      string                    `json:"last_error,omitempty"`
	Phases         []DecommissionPhaseStatus `json:"phases"`
	StatusURL      string                    `json:"status_url"`
}

// phaseOrder is the canonical order phases are rendered in the API response.
// We keep this in lockstep with the reconciler's execution order so the UI
// can render a left-to-right progress bar.
var phaseOrder = []string{
	tasks.PhaseCleanupManagedSide,
	tasks.PhaseRevokeAgentToken,
	tasks.PhaseArchiveAudit,
	tasks.PhaseDeleteDependents,
	tasks.PhaseTombstoneCluster,
}

func formatPhases(raw json.RawMessage) []DecommissionPhaseStatus {
	if len(raw) == 0 {
		return formatEmptyPhases()
	}
	type phaseRecord struct {
		Status      string         `json:"status"`
		StartedAt   time.Time      `json:"started_at,omitempty"`
		CompletedAt time.Time      `json:"completed_at,omitempty"`
		Error       string         `json:"error,omitempty"`
		Detail      map[string]any `json:"detail,omitempty"`
	}
	parsed := map[string]phaseRecord{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return formatEmptyPhases()
	}
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		rec, ok := parsed[name]
		entry := DecommissionPhaseStatus{Name: name, Status: tasks.PhaseStatusPending}
		if ok {
			entry.Status = rec.Status
			if !rec.StartedAt.IsZero() {
				entry.StartedAt = rec.StartedAt.UTC().Format(time.RFC3339)
			}
			if !rec.CompletedAt.IsZero() {
				entry.CompletedAt = rec.CompletedAt.UTC().Format(time.RFC3339)
			}
			entry.Error = rec.Error
			entry.Detail = rec.Detail
		}
		out = append(out, entry)
	}
	return out
}

func formatEmptyPhases() []DecommissionPhaseStatus {
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		out = append(out, DecommissionPhaseStatus{Name: name, Status: tasks.PhaseStatusPending})
	}
	return out
}

func renderDecommission(row sqlc.ClusterDecommission, statusURL string) DecommissionStatusResponse {
	out := DecommissionStatusResponse{
		DecommissionID: row.ID.String(),
		ClusterID:      row.ClusterID.String(),
		ClusterName:    row.ClusterName,
		Status:         row.Status,
		Attempts:       row.Attempts,
		LastError:      row.LastError,
		Phases:         formatPhases(row.Phases),
		StatusURL:      statusURL,
	}
	if row.StartedAt.Valid {
		out.StartedAt = row.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.CompletedAt.Valid {
		out.CompletedAt = row.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

// Delete handles DELETE /api/v1/clusters/{id}/.
//
// Previously this hard-deleted the cluster row, leaving residue (agent WS
// tunnel still connected until timeout, managed-side resources still
// running, audit_log rows orphaned, registration tokens not revoked).
// Now the handler inserts a cluster_decommissions row and enqueues the
// reconciler — the worker walks the cleanup phases and tombstones the
// cluster row at the end. The endpoint returns 202 Accepted with the
// decommission ID + a poll URL.
//
// Idempotent: re-DELETE on a cluster with an in-flight decommission returns
// the existing row's status (202 again) rather than creating a duplicate.
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}

	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if cluster.IsLocal {
		// The local cluster represents the host this server itself runs in;
		// decommissioning it would tear down the management plane. Refuse.
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot decommission the local cluster")
		return
	}

	// Migration 057: refuse or defer when an active maintenance window
	// applies to cluster.delete on this cluster's labels.
	if EnforceMaintenanceWindow(w, r, h.maintenanceGate, "cluster.delete",
		MaintenanceGateClusterLabels(cluster),
		pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{}) {
		return
	}

	// force=true (query param) tells the reconciler to skip the managed-side
	// cleanup grace window and tombstone immediately — used when the operator
	// knows the agent is gone and wants the row removed now.
	force := queryBool(r, "force")

	// Idempotency: if there's already an in-flight or succeeded decommission
	// for this cluster, return its status rather than creating a duplicate.
	if existing, lookupErr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); lookupErr == nil {
		if existing.Status == tasks.PhaseStatusPending || existing.Status == tasks.PhaseStatusRunning || existing.Status == tasks.PhaseStatusSucceeded {
			// A force re-delete of an already in-flight decommission escalates it
			// (so a normal delete that's stuck waiting out the grace can be
			// forced through) and nudges the worker to re-run now.
			if force && !existing.Force && existing.Status != tasks.PhaseStatusSucceeded {
				escalated, forceErr := executeMutation(r, h.runTx,
					func(q ClusterMutationTx) (sqlc.ClusterDecommission, error) {
						row, mutationErr := q.SetClusterDecommissionForce(r.Context(), existing.ID)
						if mutationErr != nil {
							return sqlc.ClusterDecommission{}, mutationErr
						}
						if mutationErr = enqueueClusterDecommissionOutbox(r, q, row.ID, "force"); mutationErr != nil {
							return sqlc.ClusterDecommission{}, mutationErr
						}
						return row, nil
					},
					func(row sqlc.ClusterDecommission) mutationAuditEvent {
						return mutationAuditEvent{
							action: "cluster.decommission.forced", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
							status: http.StatusAccepted, detail: map[string]any{"decommission_id": row.ID.String()},
						}
					})
				if forceErr != nil {
					respondTransactionalMutationError(w, r, forceErr, http.StatusInternalServerError, apierror.UpdateError, "Failed to force cluster decommission")
					return
				}
				existing = escalated
			}
			statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
			RespondJSON(w, http.StatusAccepted, renderDecommission(existing, statusURL))
			return
		}
		// `failed` → fall through and create a fresh decommission row; the
		// previous attempt remains in the DB for forensics.
	}

	requestedBy := pgtype.UUID{}
	if userID := currentUserUUID(r); userID.Valid {
		requestedBy = userID
	}

	params := sqlc.CreateClusterDecommissionParams{
		ClusterID:     id,
		RequestedByID: requestedBy,
		ClusterName:   cluster.Name,
		Force:         force,
	}
	row, err := executeMutation(r, h.runTx,
		func(q ClusterMutationTx) (sqlc.ClusterDecommission, error) {
			created, mutationErr := q.CreateClusterDecommission(r.Context(), params)
			if mutationErr != nil {
				return sqlc.ClusterDecommission{}, mutationErr
			}
			if mutationErr = enqueueClusterDecommissionOutbox(r, q, created.ID, "request"); mutationErr != nil {
				return sqlc.ClusterDecommission{}, mutationErr
			}
			return created, nil
		},
		func(created sqlc.ClusterDecommission) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.decommission.requested", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusAccepted, detail: map[string]any{"decommission_id": created.ID.String()},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateDecommissionFailed, "Failed to enqueue cluster decommission")
		return
	}
	h.publishEvent("cluster.decommission_enqueued", map[string]any{
		"cluster_id":      id.String(),
		"decommission_id": row.ID.String(),
	})

	h.triggerGrafanaFolders()
	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusAccepted, renderDecommission(row, statusURL))
}

func enqueueClusterDecommissionOutbox(r *http.Request, q tasks.TaskOutboxWriter, decommissionID uuid.UUID, operation string) error {
	task, err := tasks.NewClusterDecommissionTask(decommissionID)
	if err != nil {
		return err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), reqctx.CorrelationID(r.Context()))
	task = asynq.NewTask(task.Type(), payload)
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestKey == "" {
		requestKey = reqctx.RequestID(r.Context())
	}
	if requestKey == "" {
		requestKey = uuid.NewString()
	}
	_, err = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
		DedupeKey: "cluster_decommission:" + audit.MutationDedupeKey(
			requestKey, operation, "cluster_decommission", decommissionID.String(),
		),
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
	})
	return err
}

// GetDecommission handles GET /api/v1/clusters/{id}/decommission/.
// Returns the latest decommission row's status (idempotent — callers can
// poll). 404 when no decommission has ever been enqueued for the cluster.
func (h *ClusterHandler) GetDecommission(w http.ResponseWriter, r *http.Request) {
	id, ok := parseClusterIDParam(w, r, "id")
	if !ok {
		return
	}
	row, err := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No decommission for cluster")
		return
	}
	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusOK, renderDecommission(row, statusURL))
}
