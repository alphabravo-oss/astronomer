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
		// Opt-in telemetry POST (migration 046). Daily at 02:30 UTC.
		// Handler short-circuits when telemetry.enabled is false.
		{"30 2 * * *", tasks.TelemetrySendType, "telemetry send (daily 02:30)"},
		// Migration 047: drain email_messages queued/failed rows into
		// real SMTP sends. 30s cadence.
		{"@every 30s", tasks.EmailDispatchType, "email dispatch (smtp drain)"},
		// Email retention sweep — daily at 03:30 (offset from the
		// 03:00 backup-retention task to spread DB load).
		{"30 3 * * *", tasks.EmailCleanupOldType, "email retention sweep (90d)"},
		// Migration 048: drain pending webhook_deliveries into HMAC-signed
		// HTTP POSTs. 15s cadence — the bus tap enqueues with next_attempt_at=now
		// so this tick is the SLA between event-fire and webhook receipt.
		{"@every 15s", tasks.WebhookDispatchType, "webhook dispatch (outbound POST drain)"},
		// Webhook delivery retention sweep — daily at 04:00 (offset from
		// the email-retention task to spread DB load).
		{"0 4 * * *", tasks.WebhookCleanupOldType, "webhook delivery retention sweep (30d)"},
		// Cluster template drift sweep (migration 049). Hourly cadence —
		// the per-cluster drift check is cheap (two JSONB diffs against
		// the snapshot) and the result feeds a UI badge, not an
		// auto-correct path.
		{"@every 1h", tasks.ClusterTemplateDriftCheckType, "cluster template drift sweep"},
		// Migration 050: drift sweep for cluster registry configs.
		// Re-applies every cluster_registry_configs row so a new
		// project namespace, an accidental Secret deletion, or a
		// worker restart mid-apply self-heals. The SSA on the Secret
		// is a no-op when state already matches, so this is cheap.
		{"@every 30m", tasks.ClusterRegistryDriftReconcileType, "cluster registry drift reconcile"},
		// Migration 052: per-cluster Velero snapshot lifecycle.
		//   - Poll: every 30s, mirror Velero status into the snapshot/restore rows.
		//   - Dispatch: every 1m, fire any scheduled snapshot whose cron has elapsed.
		//   - Cleanup: daily, drop expired terminal rows (Velero owns object-store TTL).
		{"@every 30s", tasks.ClusterSnapshotPollType, "cluster snapshot poll"},
		{"@every 1m", tasks.ClusterSnapshotDispatchScheduledType, "cluster snapshot scheduled dispatcher"},
		{"15 4 * * *", tasks.ClusterSnapshotCleanupExpiredType, "cluster snapshot expired cleanup (daily 04:15)"},
		// Migration 053: drift sweep for cloud-credential materializations.
		// Walks every row whose status != 'applied' and retries — the
		// Secret SSA is idempotent so converged rows fast-fail through
		// the apply path without a wire write.
		{"@every 30m", tasks.CloudCredentialDriftReconcileType, "cloud credentials drift reconcile"},
		// Migration 060: GitOps cluster registration sync. 60s cadence
		// matches the schema default sync_interval_seconds; per-source
		// last_synced_at gates whether each row actually executes on
		// this tick. The same handler runs the tombstone reaper at the
		// end of every successful tick.
		{"@every 60s", tasks.GitOpsSyncType, "gitops cluster registration sync"},
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
