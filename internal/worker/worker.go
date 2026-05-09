package worker

import (
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// Task type constants
const (
	TypeHealthCheck                       = "cluster:health_check"
	TypeAlertEvaluation                   = "alert:evaluate"
	TypeCatalogSync                       = "catalog:sync"
	TypeMetricsAggregation                = "metrics:aggregate"
	TypeMonitoringReconcile               = "monitoring:reconcile"
	TypeBackupExecution                   = "backup:execute"
	TypeSecurityScan                      = "security:scan"
	// Phase B5: cis-operator report ingestion. Re-enqueues itself every 30s
	// for up to ~30 min until the matching ClusterScanReport is available.
	TypeSecurityIngest                    = tasks.SecurityIngestType
	TypeNotificationSend                  = "notification:send"
	TypeAgentManifest                     = "agent:generate_manifest"
	TypeCleanupExpiredRegistrationTokens  = tasks.CleanupExpiredRegistrationTokensType
	TypeCleanupOldAlertEvents             = tasks.CleanupOldAlertEventsType
	TypeRunScheduledBackups               = tasks.RunScheduledBackupsType
	TypeEnforceBackupRetention            = tasks.EnforceBackupRetentionType
	TypeRunRestore                        = tasks.RunRestoreType
	// Phase B3: project enforcement controller. ProjectReconcile runs for a
	// single (project, cluster, namespace); ProjectReconcileAll is the
	// periodic sweep that walks every project_namespaces row using a
	// cooperative DB lease so multiple worker pods don't fight.
	TypeProjectReconcile    = tasks.ProjectReconcileType
	TypeProjectReconcileAll = tasks.ProjectReconcileAllType
)

// Worker wraps the Asynq server for processing background tasks.
type Worker struct {
	server *asynq.Server
	mux    *asynq.ServeMux
	log    *slog.Logger
}

// NewWorker creates a new Asynq-based background worker.
func NewWorker(redisURL string, log *slog.Logger) *Worker {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.Error("failed to parse redis URL, falling back to default", "error", err)
		redisOpt = asynq.RedisClientOpt{Addr: "localhost:6379"}
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
	}
}

// RegisterHandlers sets up all task handlers on the mux.
func (w *Worker) RegisterHandlers() {
	w.mux.HandleFunc(TypeHealthCheck, tasks.HandleHealthCheck)
	w.mux.HandleFunc(TypeAlertEvaluation, tasks.HandleAlertEvaluation)
	w.mux.HandleFunc(TypeCatalogSync, tasks.HandleCatalogSync)
	w.mux.HandleFunc(TypeMetricsAggregation, tasks.HandleMetricsAggregation)
	w.mux.HandleFunc(TypeMonitoringReconcile, tasks.HandleMonitoringReconcile)
	w.mux.HandleFunc(TypeBackupExecution, tasks.HandleBackupExecution)
	w.mux.HandleFunc(TypeSecurityScan, tasks.HandleSecurityScan)
	w.mux.HandleFunc(TypeSecurityIngest, tasks.HandleSecurityIngest)
	w.mux.HandleFunc(TypeNotificationSend, tasks.HandleNotificationSend)
	w.mux.HandleFunc(TypeAgentManifest, tasks.HandleAgentManifest)
	w.mux.HandleFunc(TypeCleanupExpiredRegistrationTokens, tasks.HandleCleanupRegistrationTokens)
	w.mux.HandleFunc(TypeCleanupOldAlertEvents, tasks.HandleCleanupAlertEvents)
	w.mux.HandleFunc(TypeRunScheduledBackups, tasks.HandleRunScheduledBackups)
	w.mux.HandleFunc(TypeEnforceBackupRetention, tasks.HandleEnforceBackupRetention)
	w.mux.HandleFunc(TypeRunRestore, tasks.HandleRunRestore)
	w.mux.HandleFunc(TypeProjectReconcile, tasks.HandleProjectReconcile)
	w.mux.HandleFunc(TypeProjectReconcileAll, tasks.HandleProjectReconcileAll)

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
