package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func (h *ClusterSnapshotsHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterSnapshotSchedules(r.Context(), clusterID)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.ListError, "Failed to list schedules")
		return
	}
	out := make([]ScheduleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduleToResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *ClusterSnapshotsHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || row.ClusterID != clusterID {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Schedule not found")
		return
	}
	RespondJSON(w, http.StatusOK, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Cluster not found")
		return
	}

	var req ScheduleRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "name is required")
		return
	}
	if !validVeleroResourceName(strings.ToLower(req.Name)) {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "name must be a valid DNS subdomain")
		return
	}
	if _, err := parseCronExpression(req.CronSchedule); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, fmt.Sprintf("invalid cron_schedule: %v", err))
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	params := sqlc.CreateClusterSnapshotScheduleParams{
		ClusterID:    clusterID,
		Name:         req.Name,
		CronSchedule: req.CronSchedule,
		Spec:         encodeSpec(req.Spec),
		Enabled:      enabled,
		CreatedBy:    currentUserUUID(r),
	}
	row, err := executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (sqlc.ClusterSnapshotSchedule, error) {
			return q.CreateClusterSnapshotSchedule(r.Context(), params)
		},
		func(row sqlc.ClusterSnapshotSchedule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.snapshot.schedule_created", resourceType: "cluster_snapshot_schedule",
				resourceID: row.ID.String(), resourceName: cluster.Name, status: http.StatusCreated,
				detail: map[string]any{
					"cluster_id": clusterID.String(), "name": row.Name,
					"cron_schedule": row.CronSchedule, "enabled": row.Enabled,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.CreateError, "Failed to create schedule (name conflict?)")
		return
	}

	h.publishSnapshotChanged(clusterID, row.ID, "schedule")
	RespondJSON(w, http.StatusCreated, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Cluster not found")
		return
	}
	existing, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || existing.ClusterID != clusterID {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Schedule not found")
		return
	}

	var req ScheduleRequest
	if err := decodeV1JSONBody(r, &req); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, "Invalid JSON body")
		return
	}
	if _, err := snapshotScheduleUpdateParams(scheduleID, req, existing); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.InvalidBody, err.Error())
		return
	}
	row, err := executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (sqlc.ClusterSnapshotSchedule, error) {
			locked, err := q.GetClusterSnapshotScheduleForUpdate(r.Context(), scheduleID)
			if err != nil {
				return sqlc.ClusterSnapshotSchedule{}, err
			}
			if locked.ClusterID != clusterID {
				return sqlc.ClusterSnapshotSchedule{}, pgx.ErrNoRows
			}
			params, err := snapshotScheduleUpdateParams(scheduleID, req, locked)
			if err != nil {
				return sqlc.ClusterSnapshotSchedule{}, err
			}
			return q.UpdateClusterSnapshotSchedule(r.Context(), params)
		},
		func(row sqlc.ClusterSnapshotSchedule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.snapshot.schedule_updated", resourceType: "cluster_snapshot_schedule",
				resourceID: row.ID.String(), resourceName: cluster.Name, status: http.StatusOK,
				detail: map[string]any{
					"cluster_id": clusterID.String(), "name": row.Name,
					"cron_schedule": row.CronSchedule, "enabled": row.Enabled,
				},
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Schedule not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.UpdateError, "Failed to update schedule")
		return
	}
	h.publishSnapshotChanged(clusterID, row.ID, "schedule")
	RespondJSON(w, http.StatusOK, scheduleToResponse(row))
}

func (h *ClusterSnapshotsHandler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	clusterID, scheduleID, ok := parseClusterAndScheduleIDs(w, r)
	if !ok {
		return
	}
	existing, err := h.queries.GetClusterSnapshotScheduleByID(r.Context(), scheduleID)
	if err != nil || existing.ClusterID != clusterID {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Schedule not found")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterSnapshotMutationTx) (sqlc.ClusterSnapshotSchedule, error) {
			locked, err := q.GetClusterSnapshotScheduleForUpdate(r.Context(), scheduleID)
			if err != nil {
				return sqlc.ClusterSnapshotSchedule{}, err
			}
			if locked.ClusterID != clusterID {
				return sqlc.ClusterSnapshotSchedule{}, pgx.ErrNoRows
			}
			if err := q.DeleteClusterSnapshotSchedule(r.Context(), scheduleID); err != nil {
				return sqlc.ClusterSnapshotSchedule{}, err
			}
			return locked, nil
		},
		func(row sqlc.ClusterSnapshotSchedule) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.snapshot.schedule_deleted", resourceType: "cluster_snapshot_schedule",
				resourceID: scheduleID.String(), status: http.StatusNoContent,
				detail: map[string]any{"cluster_id": clusterID.String(), "name": row.Name},
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Schedule not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.DeleteError, "Failed to delete schedule")
		return
	}
	h.publishSnapshotChanged(clusterID, scheduleID, "schedule")
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------
// /velero-status/ — installed? BSL ready?
// ----------------------------------------------------------------------
