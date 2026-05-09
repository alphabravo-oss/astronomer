package worker

import (
	"log/slog"

	"github.com/hibiken/asynq"
)

// Scheduler manages periodic tasks, replacing Celery Beat.
type Scheduler struct {
	scheduler *asynq.Scheduler
	log       *slog.Logger
}

// NewScheduler creates a new periodic task scheduler.
func NewScheduler(redisURL string, log *slog.Logger) *Scheduler {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.Error("failed to parse redis URL, falling back to default", "error", err)
		redisOpt = asynq.RedisClientOpt{Addr: "localhost:6379"}
	}

	s := asynq.NewScheduler(redisOpt, nil)

	return &Scheduler{
		scheduler: s,
		log:       log,
	}
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
		{"@every 1h", TypeRunScheduledBackups, "run scheduled backups"},
		{"0 3 * * *", TypeEnforceBackupRetention, "enforce backup retention (daily 03:00)"},
		// Phase B3: re-apply project ResourceQuota / LimitRange / NetworkPolicy
		// across every project_namespaces row. The handler also enqueues a
		// per-namespace reconcile on AddNamespace; this sweep covers drift
		// and missed-delivery cases.
		{"@every 5m", TypeProjectReconcileAll, "project enforcement sweep"},
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
