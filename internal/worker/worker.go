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
	// ArgoCD managed-cluster label refresh. Enqueued by ClustersHandler.Update
	// after a labels mutation; the worker re-stamps the astronomer.io/label-*
	// keys on every upstream ArgoCD cluster Secret this cluster is registered
	// into. Idempotent — skips the PATCH when the desired labels already match.
	TypeArgoCDRefreshManagedClusterLabels = tasks.ArgoCDRefreshManagedClusterLabelsType
	// Telemetry sender — opt-in nightly POST. Migration 046.
	TypeTelemetrySend = tasks.TelemetrySendType
	// Migration 047: SMTP email dispatch + retention.
	TypeEmailDispatch   = tasks.EmailDispatchType
	TypeEmailCleanupOld = tasks.EmailCleanupOldType
	// Migration 048: outbound webhook dispatch + retention.
	TypeWebhookDispatch   = tasks.WebhookDispatchType
	TypeWebhookCleanupOld = tasks.WebhookCleanupOldType
	// Migration 049: cluster template apply + drift sweep.
	TypeClusterTemplateApply      = tasks.ClusterTemplateApplyType
	TypeClusterTemplateDriftCheck = tasks.ClusterTemplateDriftCheckType
	// Migration 050: cluster registry credentials → in-cluster
	// dockerconfigjson Secret + default-SA imagePullSecrets patch.
	// ClusterApplyRegistrySecret runs for a single registry row
	// (enqueued by the handler on POST/PUT/DELETE); the
	// ClusterRegistryDriftReconcile sweep is the every-30m fallback.
	TypeClusterApplyRegistrySecret    = tasks.ClusterApplyRegistrySecretType
	TypeClusterRegistryDriftReconcile = tasks.ClusterRegistryDriftReconcileType
	// Migration 052: per-cluster Velero snapshot lifecycle. Three task
	// types share one querier + driver wiring; see
	// tasks.ConfigureClusterSnapshotTasks.
	TypeClusterSnapshotPoll              = tasks.ClusterSnapshotPollType
	TypeClusterSnapshotDispatchScheduled = tasks.ClusterSnapshotDispatchScheduledType
	TypeClusterSnapshotCleanupExpired    = tasks.ClusterSnapshotCleanupExpiredType
	// Migration 053: cloud credentials → in-cluster k8s Secret. The
	// CloudCredentialMaterialize task runs for one (credential, cluster,
	// namespace) tuple; the CloudCredentialDriftReconcile sweep walks
	// every materialization not in the "applied" state on a 30m cadence.
	TypeCloudCredentialMaterialize    = tasks.CloudCredentialMaterializeType
	TypeCloudCredentialDriftReconcile = tasks.CloudCredentialDriftReconcileType
	// Migration 055: SIEM forwarder dispatch + retention sweep. The
	// dispatcher drains every enabled forwarder's queue every 2s; the
	// cleanup task prunes queue rows older than 7 days regardless of
	// forwarder status.
	TypeSIEMDispatch   = tasks.SIEMDispatchType
	TypeSIEMCleanupOld = tasks.SIEMCleanupOldType
	// Migration 056: fleet operations orchestrator. Periodic task that
	// drives every pending/running fleet_operations row toward a
	// terminal status — evaluates the selector at launch, dispatches
	// up to max_concurrent per-cluster sub-operations, polls them,
	// applies the abort-on-error policy. Idempotent: re-running a tick
	// on the same operation won't re-fire sub-operations that already
	// completed.
	TypeFleetOrchestrate = tasks.FleetOrchestrateType
	// Migration 057: maintenance window deferred-op dispatcher.
	TypeDispatchDeferred = tasks.DispatchDeferredType
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
	w.mux.HandleFunc(TypeArgoCDRefreshManagedClusterLabels, instrumentTask(TypeArgoCDRefreshManagedClusterLabels, tasks.HandleArgoCDRefreshManagedClusterLabels))
	w.mux.HandleFunc(tasks.RefreshGroupSyncMetricsType, instrumentTask(tasks.RefreshGroupSyncMetricsType, tasks.HandleRefreshGroupSyncMetrics))
	w.mux.HandleFunc(TypeTelemetrySend, instrumentTask(TypeTelemetrySend, tasks.HandleTelemetrySend))
	w.mux.HandleFunc(TypeEmailDispatch, instrumentTask(TypeEmailDispatch, tasks.HandleEmailDispatch))
	w.mux.HandleFunc(TypeEmailCleanupOld, instrumentTask(TypeEmailCleanupOld, tasks.HandleEmailCleanupOld))
	w.mux.HandleFunc(TypeWebhookDispatch, instrumentTask(TypeWebhookDispatch, tasks.HandleWebhookDispatch))
	w.mux.HandleFunc(TypeWebhookCleanupOld, instrumentTask(TypeWebhookCleanupOld, tasks.HandleWebhookCleanupOld))
	w.mux.HandleFunc(TypeClusterTemplateApply, instrumentTask(TypeClusterTemplateApply, tasks.HandleClusterTemplateApply))
	w.mux.HandleFunc(TypeClusterTemplateDriftCheck, instrumentTask(TypeClusterTemplateDriftCheck, tasks.HandleClusterTemplateDriftCheck))
	w.mux.HandleFunc(TypeClusterApplyRegistrySecret, instrumentTask(TypeClusterApplyRegistrySecret, tasks.HandleClusterApplyRegistrySecret))
	w.mux.HandleFunc(TypeClusterRegistryDriftReconcile, instrumentTask(TypeClusterRegistryDriftReconcile, tasks.HandleClusterRegistryDriftReconcile))
	w.mux.HandleFunc(TypeClusterSnapshotPoll, instrumentTask(TypeClusterSnapshotPoll, tasks.HandleClusterSnapshotPoll))
	w.mux.HandleFunc(TypeClusterSnapshotDispatchScheduled, instrumentTask(TypeClusterSnapshotDispatchScheduled, tasks.HandleClusterSnapshotDispatchScheduled))
	w.mux.HandleFunc(TypeClusterSnapshotCleanupExpired, instrumentTask(TypeClusterSnapshotCleanupExpired, tasks.HandleClusterSnapshotCleanupExpired))
	w.mux.HandleFunc(TypeCloudCredentialMaterialize, instrumentTask(TypeCloudCredentialMaterialize, tasks.HandleCloudCredentialMaterialize))
	w.mux.HandleFunc(TypeCloudCredentialDriftReconcile, instrumentTask(TypeCloudCredentialDriftReconcile, tasks.HandleCloudCredentialDriftReconcile))
	w.mux.HandleFunc(TypeSIEMDispatch, instrumentTask(TypeSIEMDispatch, tasks.HandleSIEMDispatch))
	w.mux.HandleFunc(TypeSIEMCleanupOld, instrumentTask(TypeSIEMCleanupOld, tasks.HandleSIEMCleanupOld))
	w.mux.HandleFunc(TypeFleetOrchestrate, instrumentTask(TypeFleetOrchestrate, tasks.HandleFleetOrchestrate))
	w.mux.HandleFunc(TypeDispatchDeferred, instrumentTask(TypeDispatchDeferred, tasks.HandleDispatchDeferred))

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
