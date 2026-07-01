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
	// selector relies on that ordering to keep the newest N. We page through
	// the FULL set rather than a bounded first page — otherwise, once the
	// fleet accumulates more than one page of backup rows, per-schedule
	// count retention would silently stop pruning the tail (and its
	// object-store bytes would leak) because those older rows never become
	// candidates.
	backups, err := h.listAllBackups(ctx)
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
// that schedule and returns the rest for deletion. `backups` must be newest-first.
//
// Attribution is by Velero's exact schedule naming convention
// ("<scheduleName>-<14-digit-timestamp>"), NOT a bare name prefix: a bare
// prefix causes one schedule to claim (and therefore prune) another's
// backups whenever the names are prefix-related — e.g. schedule "daily"
// would otherwise match "daily-critical-20260101120000", deleting the
// "daily-critical" schedule's backups (silent data loss).
func selectBackupsToPrune(schedules []sqlc.BackupSchedule, backups []sqlc.Backup) []sqlc.Backup {
	var prune []sqlc.Backup
	for _, s := range schedules {
		if s.RetentionCount <= 0 || strings.TrimSpace(s.VeleroScheduleName) == "" {
			continue
		}
		kept := 0
		for _, b := range backups {
			if b.Status != "completed" || !isScheduleOwnedBackup(b.VeleroBackupName, s.VeleroScheduleName) {
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

// isScheduleOwnedBackup reports whether backupName was produced by the Velero
// Schedule scheduleName. Velero names a schedule-created backup
// "<scheduleName>-<timestamp>" where timestamp is time.Format("20060102150405")
// — exactly 14 digits. Requiring the suffix to be that timestamp (rather than
// accepting any prefix match) prevents cross-schedule attribution when two
// schedule names are prefix-related.
func isScheduleOwnedBackup(backupName, scheduleName string) bool {
	prefix := scheduleName + "-"
	if !strings.HasPrefix(backupName, prefix) {
		return false
	}
	return isVeleroTimestamp(backupName[len(prefix):])
}

// isVeleroTimestamp reports whether s is a Velero backup timestamp suffix:
// exactly 14 decimal digits (YYYYMMDDhhmmss).
func isVeleroTimestamp(s string) bool {
	if len(s) != 14 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// listAllBackups pages through ListBackups (newest-first) and returns the
// complete set, so count-based retention sees every schedule-owned backup
// rather than only the newest page. A hard page cap bounds the scan against
// a pathological row count.
func (h *BackupHandler) listAllBackups(ctx context.Context) ([]sqlc.Backup, error) {
	const pageSize = 1000
	const maxPages = 1000 // 1M rows ceiling — a safety valve, not an expected bound
	var all []sqlc.Backup
	for page := 0; page < maxPages; page++ {
		batch, err := h.queries.ListBackups(ctx, sqlc.ListBackupsParams{
			Limit:  pageSize,
			Offset: int32(page * pageSize),
		})
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < pageSize {
			break
		}
	}
	return all, nil
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
