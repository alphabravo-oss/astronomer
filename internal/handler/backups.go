package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// BackupQuerier abstracts the backup-related database queries needed by BackupHandler.
type BackupQuerier interface {
	// Storage configs
	GetBackupStorageConfigByID(ctx context.Context, id uuid.UUID) (sqlc.BackupStorageConfig, error)
	ListBackupStorageConfigs(ctx context.Context, arg sqlc.ListBackupStorageConfigsParams) ([]sqlc.BackupStorageConfig, error)
	CreateBackupStorageConfig(ctx context.Context, arg sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	UpdateBackupStorageConfig(ctx context.Context, arg sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	DeleteBackupStorageConfig(ctx context.Context, id uuid.UUID) error
	CountBackupStorageConfigs(ctx context.Context) (int64, error)
	// Backups
	ListBackups(ctx context.Context, arg sqlc.ListBackupsParams) ([]sqlc.Backup, error)
	ListRunningBackupsForPolling(ctx context.Context, limit int32) ([]sqlc.Backup, error)
	GetBackupByID(ctx context.Context, id uuid.UUID) (sqlc.Backup, error)
	CreateBackup(ctx context.Context, arg sqlc.CreateBackupParams) (sqlc.Backup, error)
	UpdateBackupVeleroIdentity(ctx context.Context, arg sqlc.UpdateBackupVeleroIdentityParams) error
	// UpdateBackupStarted is a CAS claim: rows==0 means another replica already
	// claimed or the row left pending/queued/created.
	UpdateBackupStarted(ctx context.Context, id uuid.UUID) (int64, error)
	UpdateBackupCompleted(ctx context.Context, arg sqlc.UpdateBackupCompletedParams) error
	UpdateBackupFailed(ctx context.Context, arg sqlc.UpdateBackupFailedParams) error
	TouchBackupPolling(ctx context.Context, id uuid.UUID) error
	DeleteBackup(ctx context.Context, id uuid.UUID) error
	CountBackups(ctx context.Context) (int64, error)
	// Schedules
	ListBackupSchedules(ctx context.Context, arg sqlc.ListBackupSchedulesParams) ([]sqlc.BackupSchedule, error)
	GetBackupScheduleByID(ctx context.Context, id uuid.UUID) (sqlc.BackupSchedule, error)
	CreateBackupSchedule(ctx context.Context, arg sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error)
	UpdateBackupSchedule(ctx context.Context, arg sqlc.UpdateBackupScheduleParams) (sqlc.BackupSchedule, error)
	DeleteBackupSchedule(ctx context.Context, id uuid.UUID) error
	CountBackupSchedules(ctx context.Context) (int64, error)
	// Restore
	ListRestoreOperations(ctx context.Context, arg sqlc.ListRestoreOperationsParams) ([]sqlc.RestoreOperation, error)
	ListRunningRestoresForPolling(ctx context.Context, limit int32) ([]sqlc.RestoreOperation, error)
	GetRestoreOperationByID(ctx context.Context, id uuid.UUID) (sqlc.RestoreOperation, error)
	CreateRestoreOperation(ctx context.Context, arg sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error)
	UpdateRestoreOperationStarted(ctx context.Context, id uuid.UUID) (int64, error)
	UpdateRestoreOperationCompleted(ctx context.Context, id uuid.UUID) error
	UpdateRestoreOperationFailed(ctx context.Context, arg sqlc.UpdateRestoreOperationFailedParams) error
	TouchRestorePolling(ctx context.Context, id uuid.UUID) error
	CountRestoreOperations(ctx context.Context) (int64, error)
}

type BackupMutationTx interface {
	audit.OutboxQuerier
	CreateBackupStorageConfig(context.Context, sqlc.CreateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	UpdateBackupStorageConfig(context.Context, sqlc.UpdateBackupStorageConfigParams) (sqlc.BackupStorageConfig, error)
	DeleteBackupStorageConfig(context.Context, uuid.UUID) error
	CreateBackup(context.Context, sqlc.CreateBackupParams) (sqlc.Backup, error)
	DeleteBackup(context.Context, uuid.UUID) error
	CreateBackupSchedule(context.Context, sqlc.CreateBackupScheduleParams) (sqlc.BackupSchedule, error)
	UpdateBackupSchedule(context.Context, sqlc.UpdateBackupScheduleParams) (sqlc.BackupSchedule, error)
	DeleteBackupSchedule(context.Context, uuid.UUID) error
	CreateRestoreOperation(context.Context, sqlc.CreateRestoreOperationParams) (sqlc.RestoreOperation, error)
	CreateRestoreOperationIdempotent(context.Context, sqlc.CreateRestoreOperationIdempotentParams) (sqlc.RestoreOperation, error)
}

type backupRunTxFunc func(context.Context, func(BackupMutationTx) error) error

// BackupHandler handles backup endpoints (storage configs, backups, schedules, restores).
//
// Phase B2 wires Velero as the engine: the row in our DB is the source of
// desired state but never the source of truth for completion — that lives on
// the Velero CRs in each cluster, which we round-trip through the existing
// tunnel K8sRequester.
type BackupHandler struct {
	queries    BackupQuerier
	encryptor  *auth.Encryptor
	requester  K8sRequester
	httpClient *http.Client
	log        *slog.Logger
	authz      authorizationSupport
	bus        *events.Bus
	runTx      backupRunTxFunc
}

func (h *BackupHandler) SetRunTx(runTx backupRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *BackupHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// NewBackupHandler creates a new backup handler.
func NewBackupHandler(queries BackupQuerier) *BackupHandler {
	return &BackupHandler{
		queries: queries,
		log:     slog.Default(),
		// SEC-03: S3 connectivity probe dials operator-supplied endpoints;
		// SafeClient enforces public-IP at dial time (not GuardPublicHost alone).
		httpClient: httpclient.SafeClient(15 * time.Second),
	}
}

// SetEventBus wires the SSE bus for backup.changed liveness events (P4.5).
// Optional: publishers are fire-and-forget and nil-safe.
func (h *BackupHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishBackupChanged emits the metadata-only backup.changed event after a
// successful DB write. kind discriminates backup|restore|schedule.
func (h *BackupHandler) publishBackupChanged(clusterID pgtype.UUID, id uuid.UUID, kind string) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "backup", nullableUUIDString(clusterID), id.String(), map[string]any{"kind": kind})
}

// SetAuthorization wires the RBAC engine + binding querier used to enforce
// ResourceBackups permission on every handler. Until this is called the handler
// fails closed for authenticated callers (bindingsForContext returns a
// not-configured error → 500) rather than allowing unauthenticated access.
func (h *BackupHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// authorizeBackup gates an action against ResourceBackups. Cluster-scoped rows
// are checked against the caller's grant on that cluster; unscoped (global) rows
// require a global backups grant. It writes the error response and returns false
// when the caller is not permitted.
func (h *BackupHandler) authorizeBackup(w http.ResponseWriter, r *http.Request, clusterID pgtype.UUID, verb rbac.Verb) bool {
	if clusterID.Valid {
		return h.authz.authorizeClusterAction(w, r, uuid.UUID(clusterID.Bytes), rbac.ResourceBackups, verb)
	}
	return h.authz.authorizeGlobalAction(w, r, rbac.ResourceBackups, verb)
}

// SetEncryptor wires the Fernet encryptor used to round-trip cloud credentials
// into BackupStorageConfig.encrypted_credentials. New credential-bearing
// writes fail closed when it is absent; legacy plaintext columns are read-only
// compatibility data and are never populated by this handler.
func (h *BackupHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetK8sRequester wires the tunnel-backed Kubernetes API proxy. Without it,
// the handler runs in degraded mode: storage/schedule/backup/restore writes
// still hit our DB but no Velero CR is applied. This keeps the test surface
// usable when no agent is connected.
func (h *BackupHandler) SetK8sRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetHTTPClient overrides the HTTP client used by TestStorageConfig for
// connectivity probes. Tests use httptest.NewServer + its Client to avoid
// hitting the live network.
func (h *BackupHandler) SetHTTPClient(client *http.Client) {
	if h == nil || client == nil {
		return
	}
	h.httpClient = client
}

// SetLogger overrides the structured logger used by this handler.
func (h *BackupHandler) SetLogger(log *slog.Logger) {
	if h == nil || log == nil {
		return
	}
	h.log = log
}

// ControllerStatus summarizes backup subsystem operational state.
func (h *BackupHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	// Aggregates every cluster's backup state, so it needs a global read grant.
	if !h.authz.authorizeGlobalAction(w, r, rbac.ResourceBackups, rbac.VerbRead) {
		return
	}
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load backups")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *BackupHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	backups, err := h.queries.ListBackups(ctx, sqlc.ListBackupsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	schedules, err := h.queries.ListBackupSchedules(ctx, sqlc.ListBackupSchedulesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	restores, err := h.queries.ListRestoreOperations(ctx, sqlc.ListRestoreOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	storageCount, _ := h.queries.CountBackupStorageConfigs(ctx)
	backupCounts := map[string]int{}
	restoreCounts := map[string]int{}
	runningBackups := 0
	runningRestores := 0
	failedBackups := 0
	failedRestores := 0
	for _, backup := range backups {
		backupCounts[backup.Status]++
		switch backup.Status {
		case "pending", "running", "in_progress":
			runningBackups++
		case "failed", "error":
			failedBackups++
		}
	}
	enabledSchedules := 0
	for _, schedule := range schedules {
		if schedule.Enabled {
			enabledSchedules++
		}
	}
	for _, restore := range restores {
		restoreCounts[restore.Status]++
		switch restore.Status {
		case "pending", "running", "in_progress":
			runningRestores++
		case "failed", "error":
			failedRestores++
		}
	}
	health := "healthy"
	reasons := make([]string, 0, 2)
	if failedBackups > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_backups_present")
	}
	if failedRestores > 0 {
		health = "degraded"
		reasons = append(reasons, "failed_restores_present")
	}
	return map[string]any{
		"reconciler": map[string]any{
			"enabled": h.requester != nil,
			"engine":  "velero",
		},
		"health":        health,
		"healthReasons": reasons,
		"storage": map[string]any{
			"count": storageCount,
		},
		"backups": map[string]any{
			"total":        len(backups),
			"runningCount": runningBackups,
			"failedCount":  failedBackups,
			"statuses":     backupCounts,
		},
		"schedules": map[string]any{
			"total":        len(schedules),
			"enabledCount": enabledSchedules,
		},
		"restores": map[string]any{
			"total":        len(restores),
			"runningCount": runningRestores,
			"failedCount":  failedRestores,
		},
	}, nil
}
