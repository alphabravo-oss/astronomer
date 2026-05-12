package worker

import (
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// Scheduler manages periodic tasks, replacing Celery Beat.
type Scheduler struct {
	scheduler *asynq.Scheduler
	log       *slog.Logger
}

// NewScheduler creates a new periodic task scheduler.
//
// As with NewWorker, an invalid REDIS_URL is fail-fast — silent localhost
// fallback was a production footgun.
func NewScheduler(redisURL string, log *slog.Logger) (*Scheduler, error) {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}

	s := asynq.NewScheduler(redisOpt, nil)

	return &Scheduler{
		scheduler: s,
		log:       log,
	}, nil
}

// RegisterPeriodicTasks sets up all cron-based tasks matching the Python Celery Beat schedule.
func (s *Scheduler) RegisterPeriodicTasks() error {
	entries := []struct {
		cron     string
		taskType string
		desc     string
	}{
		{"@every 60s", TypeHealthCheck, "cluster health check"},
		{"@every 60s", TypeAlertEvaluation, "alert rule evaluation"},
		{"@every 6h", TypeCatalogSync, "catalog sync"},
		{"@every 5m", TypeMetricsAggregation, "metrics aggregation"},
		{"@every 2m", TypeMonitoringReconcile, "monitoring reconciliation"},
		{"@every 6h", TypeCleanupExpiredRegistrationTokens, "cleanup expired registration tokens"},
		{"0 2 * * *", TypeCleanupOldAlertEvents, "cleanup old alert events (daily 02:00)"},
		{"0 1 * * *", TypeEnsureAuditLogPartitions, "ensure audit_log monthly partitions (daily 01:00)"},
		{"30 1 * * *", TypeEnforceAuditLogRetention, "enforce audit_log retention (daily 01:30)"},
		{"@every 1h", TypeRunScheduledBackups, "run scheduled backups"},
		{"0 3 * * *", TypeEnforceBackupRetention, "enforce backup retention (daily 03:00)"},
		// Phase B3: re-apply project ResourceQuota / LimitRange / NetworkPolicy
		// across every project_namespaces row. The handler also enqueues a
		// per-namespace reconcile on AddNamespace; this sweep covers drift
		// and missed-delivery cases.
		{"@every 5m", TypeProjectReconcileAll, "project enforcement sweep"},
		// Cluster decommission sweep. The DELETE handler enqueues a single
		// reconciler invocation immediately; the periodic sweep here picks
		// up rows whose worker process crashed mid-phase (status=running)
		// and rows that failed and need a retry (status=failed→running).
		{"@every 1m", TypeClusterDecommissionAll, "cluster decommission sweep"},
		// Recompute the auth_group_bindings gauge so it doesn't go
		// stale between SSO login runs. Cheap — three COUNT(*)s.
		{"@every 5m", tasks.RefreshGroupSyncMetricsType, "refresh group-sync binding gauge"},
		// Opt-in telemetry POST (migration 046). Daily at 02:30 UTC —
		// off-peak across our typical fleet. Handler short-circuits
		// when telemetry.enabled is false, so an opt-out install
		// pays one DB read per night and nothing else.
		{"30 2 * * *", tasks.TelemetrySendType, "telemetry send (daily 02:30)"},
	}

	for _, e := range entries {
		task := asynq.NewTask(e.taskType, nil)
		entryID, err := s.scheduler.Register(e.cron, task)
		if err != nil {
			s.log.Error("failed to register periodic task", "task", e.taskType, "error", err)
			return err
		}
		s.log.Info("registered periodic task", "task", e.desc, "schedule", e.cron, "entry_id", entryID)
	}

	return nil
}

// Start begins the scheduler. This blocks until Shutdown is called.
func (s *Scheduler) Start() error {
	s.log.Info("starting scheduler")
	return s.scheduler.Start()
}

// Shutdown stops the scheduler.
func (s *Scheduler) Shutdown() {
	s.log.Info("shutting down scheduler")
	s.scheduler.Shutdown()
}
