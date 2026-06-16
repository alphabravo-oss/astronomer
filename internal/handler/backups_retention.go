package handler

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// enforceScheduleRetention prunes schedule-created Velero backups that exceed
// each schedule's retention_count (keep newest N completed). Velero's spec.ttl
// only does TIME-based expiry, so without this the count-based retention the
// scheduling UI collects ("keep N backups") would silently do nothing.
//
// A backup created by a Velero Schedule is named "<scheduleName>-<timestamp>",
// which the reconciler ingests into the backups table verbatim, so we attribute
// a backup to its schedule by that name prefix. Runs as a reconciler step on
// the server (where the tunnel K8s requester lives).
func (h *BackupHandler) enforceScheduleRetention(ctx context.Context) {
	if h == nil || h.queries == nil || h.requester == nil {
		return
	}
	schedules, err := h.queries.ListBackupSchedules(ctx, sqlc.ListBackupSchedulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		h.logReconcileError("retention: list schedules", err)
		return
	}
	// ListBackups returns newest-first (ORDER BY created_at DESC); the prune
	// selector relies on that ordering to keep the newest N.
	backups, err := h.queries.ListBackups(ctx, sqlc.ListBackupsParams{Limit: 1000, Offset: 0})
	if err != nil {
		h.logReconcileError("retention: list backups", err)
		return
	}
	for _, b := range selectBackupsToPrune(schedules, backups) {
		if derr := h.deleteVeleroBackupByName(ctx, b); derr != nil {
			// Fall through and still drop the DB row so the count converges;
			// Velero's BSL TTL / orphan sweep reclaims the object-store bytes.
			h.logReconcileError("retention: velero delete", derr, "backup", b.VeleroBackupName)
		}
		if derr := h.queries.DeleteBackup(ctx, b.ID); derr != nil {
			h.logReconcileError("retention: db delete", derr, "backup_id", b.ID.String())
			continue
		}
		if h.log != nil {
			h.log.Info("retention: pruned backup beyond retention_count",
				"backup", b.VeleroBackupName, "backup_id", b.ID.String())
		}
	}
}

// selectBackupsToPrune is the pure selection step: for every schedule that caps
// by count, it keeps the newest retention_count COMPLETED backups attributed to
// that schedule (by velero name prefix) and returns the rest for deletion.
// `backups` must be newest-first.
func selectBackupsToPrune(schedules []sqlc.BackupSchedule, backups []sqlc.Backup) []sqlc.Backup {
	var prune []sqlc.Backup
	for _, s := range schedules {
		if s.RetentionCount <= 0 || strings.TrimSpace(s.VeleroScheduleName) == "" {
			continue
		}
		prefix := s.VeleroScheduleName + "-"
		kept := 0
		for _, b := range backups {
			if b.Status != "completed" || !strings.HasPrefix(b.VeleroBackupName, prefix) {
				continue
			}
			kept++
			if kept > int(s.RetentionCount) {
				prune = append(prune, b)
			}
		}
	}
	return prune
}

// deleteVeleroBackupByName issues a Velero DeleteBackupRequest CR for a backup
// through the agent tunnel (fire-and-forget; Velero reclaims storage async).
func (h *BackupHandler) deleteVeleroBackupByName(ctx context.Context, b sqlc.Backup) error {
	if h.requester == nil || strings.TrimSpace(b.VeleroBackupName) == "" || !b.ClusterID.Valid {
		return nil
	}
	suffix, _ := randomHex(4)
	dbrName := truncateName(b.VeleroBackupName+"-delete-"+suffix, 253)
	body := renderDeleteBackupRequest(dbrName, b.VeleroNamespace, b.VeleroBackupName)
	return createVeleroDeleteBackupRequest(ctx, h.requester, uuid.UUID(b.ClusterID.Bytes).String(), body)
}
