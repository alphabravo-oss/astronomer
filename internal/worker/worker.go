package worker

import (
	"fmt"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// Task type constants
const (
	TypeHealthCheck         = "cluster:health_check"
	TypeAlertEvaluation     = "alert:evaluate"
	TypeCatalogSync         = "catalog:sync"
	TypeMetricsAggregation  = "metrics:aggregate"
	TypeMonitoringReconcile = "monitoring:reconcile"
	TypeBackupExecution     = "backup:execute"
	TypeSecurityScan        = "security:scan"
	// Phase B5: cis-operator report ingestion. Re-enqueues itself every 30s
	// for up to ~30 min until the matching ClusterScanReport is available.
	TypeSecurityIngest                   = tasks.SecurityIngestType
	TypeNotificationSend                 = "notification:send"
	TypeAgentManifest                    = "agent:generate_manifest"
	TypeCleanupExpiredRegistrationTokens = tasks.CleanupExpiredRegistrationTokensType
	TypeCleanupOldAlertEvents            = tasks.CleanupOldAlertEventsType
	TypeEnsureAuditLogPartitions         = tasks.EnsureAuditLogPartitionsType
	TypeEnforceAuditLogRetention         = tasks.EnforceAuditLogRetentionType
	TypeRunScheduledBackups              = tasks.RunScheduledBackupsType
	TypeEnforceBackupRetention           = tasks.EnforceBackupRetentionType
	TypeRunRestore                       = tasks.RunRestoreType
	// Phase B3: project enforcement controller. ProjectReconcile runs for a
	// single (project, cluster, namespace); ProjectReconcileAll is the
	// periodic sweep that walks every project_namespaces row using a
	// cooperative DB lease so multiple worker pods don't fight.
	TypeProjectReconcile    = tasks.ProjectReconcileType
	TypeProjectReconcileAll = tasks.ProjectReconcileAllType
	// Cluster decommission controller. ClusterDecommission runs a single
	// reconciliation (enqueued by the DELETE handler); ClusterDecommissionAll
	// is the periodic sweep that picks up rows whose worker crashed.
	TypeClusterDecommission    = tasks.ClusterDecommissionType
	TypeClusterDecommissionAll = tasks.ClusterDecommissionAllType
)

// Worker wraps the Asynq server for processing background tasks.
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	log    *slog.Logger
}

// NewWorker creates a new Asynq-based background worker.
//
// An invalid REDIS_URL is fail-fast: returns a non-nil error rather than
// silently falling back to localhost:6379. The previous fallback was a
// footgun in air-gapped or split-network production clusters — the worker
// would come up, fail every redis op invisibly, and take hours to
// diagnose. Now a bad URL surfaces at process start.
func NewWorker(redisURL string, log *slog.Logger) (*Worker, error) {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	})

	return &Worker{
		server: srv,
		mux:    asynq.NewServeMux(),
		log:    log,
	}, nil
}

// RegisterHandlers sets up all task handlers on the mux.
func (w *Worker) RegisterHandlers() {
	w.mux.HandleFunc(TypeHealthCheck, instrumentTask(TypeHealthCheck, tasks.HandleHealthCheck))
	w.mux.HandleFunc(TypeAlertEvaluation, instrumentTask(TypeAlertEvaluation, tasks.HandleAlertEvaluation))
	w.mux.HandleFunc(TypeCatalogSync, instrumentTask(TypeCatalogSync, tasks.HandleCatalogSync))
	w.mux.HandleFunc(TypeMetricsAggregation, instrumentTask(TypeMetricsAggregation, tasks.HandleMetricsAggregation))
	w.mux.HandleFunc(TypeMonitoringReconcile, instrumentTask(TypeMonitoringReconcile, tasks.HandleMonitoringReconcile))
	w.mux.HandleFunc(TypeBackupExecution, instrumentTask(TypeBackupExecution, tasks.HandleBackupExecution))
	w.mux.HandleFunc(TypeSecurityScan, instrumentTask(TypeSecurityScan, tasks.HandleSecurityScan))
	w.mux.HandleFunc(TypeSecurityIngest, instrumentTask(TypeSecurityIngest, tasks.HandleSecurityIngest))
	w.mux.HandleFunc(TypeNotificationSend, instrumentTask(TypeNotificationSend, tasks.HandleNotificationSend))
	w.mux.HandleFunc(TypeAgentManifest, instrumentTask(TypeAgentManifest, tasks.HandleAgentManifest))
	w.mux.HandleFunc(TypeCleanupExpiredRegistrationTokens, instrumentTask(TypeCleanupExpiredRegistrationTokens, tasks.HandleCleanupRegistrationTokens))
	w.mux.HandleFunc(TypeCleanupOldAlertEvents, instrumentTask(TypeCleanupOldAlertEvents, tasks.HandleCleanupAlertEvents))
	w.mux.HandleFunc(TypeEnsureAuditLogPartitions, instrumentTask(TypeEnsureAuditLogPartitions, tasks.HandleEnsureAuditLogPartitions))
	w.mux.HandleFunc(TypeEnforceAuditLogRetention, instrumentTask(TypeEnforceAuditLogRetention, tasks.HandleEnforceAuditLogRetention))
	w.mux.HandleFunc(TypeRunScheduledBackups, instrumentTask(TypeRunScheduledBackups, tasks.HandleRunScheduledBackups))
	w.mux.HandleFunc(TypeEnforceBackupRetention, instrumentTask(TypeEnforceBackupRetention, tasks.HandleEnforceBackupRetention))
	w.mux.HandleFunc(TypeRunRestore, instrumentTask(TypeRunRestore, tasks.HandleRunRestore))
	w.mux.HandleFunc(TypeProjectReconcile, instrumentTask(TypeProjectReconcile, tasks.HandleProjectReconcile))
	w.mux.HandleFunc(TypeProjectReconcileAll, instrumentTask(TypeProjectReconcileAll, tasks.HandleProjectReconcileAll))
	w.mux.HandleFunc(TypeClusterDecommission, instrumentTask(TypeClusterDecommission, tasks.HandleClusterDecommission))
	w.mux.HandleFunc(TypeClusterDecommissionAll, instrumentTask(TypeClusterDecommissionAll, tasks.HandleClusterDecommissionAll))

	w.log.Info("registered all task handlers")
}

// Start begins processing tasks. This blocks until Shutdown is called.
func (w *Worker) Start() error {
	w.log.Info("starting worker")
	return w.server.Start(w.mux)
}

// Shutdown gracefully stops the worker.
func (w *Worker) Shutdown() {
	w.log.Info("shutting down worker")
	w.server.Shutdown()
}
