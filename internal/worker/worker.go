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
	// ArgoCD auto-adoption. Enqueued when an agent connects and swept
	// periodically so every live Astronomer-managed cluster is present in
	// built-in ArgoCD with a cluster-scoped proxy credential.
	TypeArgoCDAutoRegisterCluster = tasks.ArgoCDAutoRegisterClusterType
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
	TypePlaintextCredentialMigration  = tasks.PlaintextCredentialMigrationType
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
	// Migration 092: durable Postgres task outbox dispatcher. This is the
	// retry bridge from committed DB task intents into Redis/Asynq.
	TypeTaskOutboxDispatch = tasks.TaskOutboxDispatchType
	// Migration 065 / sprint 17: in-browser kubectl shell reaper.
	// 60s cadence — see internal/worker/tasks/kubectl_session_reap.go.
	TypeKubectlSessionReap = tasks.KubectlSessionReapType
	// Migration 068 / sprint 18: NetworkPolicy template reconciler.
	//  - Apply: every 5m (+ on-demand from the handler) — drains pending/
	//    failed/drifting rows, server-side-applies the rendered manifest.
	//  - DriftCheck: every 30m — GET the live NetworkPolicy and mark
	//    drifting when labels diverge from the managed-by marker.
	TypeNetworkPolicyApply      = tasks.NetworkPolicyApplyType
	TypeNetworkPolicyDriftCheck = tasks.NetworkPolicyDriftCheckType
	// Sprint 069: CRD-mirror v2 stale-row prune.
	TypeCrdMirrorPruneStale = tasks.CrdMirrorPruneStaleType
	// T6.069: gauge populator for astronomer_crd_mirror_rows.
	TypeCrdMirrorGaugePopulate = tasks.CrdMirrorGaugePopulateType
	// CRD ownership drift check. Compares CRD-owned Postgres rows against
	// their stored Kubernetes external refs and surfaces missing CRs via
	// cluster_conditions.
	TypeCRDOwnershipDriftCheck = tasks.CRDOwnershipDriftCheckType
	// Migration 070: apiserver allow-list reconciler. Three task types
	// share one querier + registry wiring; see
	// tasks.ConfigureApiserverAllowlistReconcile.
	//   - Reconcile         : per-cluster reconcile (enqueued by handler
	//                         and by the periodic ReconcileAll sweep).
	//   - ReconcileAll      : every-15m sweep over every active row.
	//   - CleanupSnapshots  : daily 90d retention prune on snapshots.
	TypeApiserverAllowlistReconcile        = tasks.ApiserverAllowlistReconcileType
	TypeApiserverAllowlistReconcileAll     = tasks.ApiserverAllowlistReconcileAllType
	TypeApiserverAllowlistCleanupSnapshots = tasks.ApiserverAllowlistCleanupSnapshotsType
	// Sprint 072: anomaly-detection rolling baseline recompute.
	TypeAnomalyBaselineRecompute = tasks.AnomalyBaselineRecomputeType
	// P1 item 5/22: cross-cluster ("fleet-wide") anomaly baseline recompute.
	TypeXClusterAnomalyRecompute = tasks.XClusterAnomalyRecomputeType
	// Sprint 073: nightly chart-rating aggregate + co-installation matrix recompute.
	TypeChartRecommendationsRecompute = tasks.ChartRecommendationsRecomputeType
	// P1 item 16/22: tool drift reconciliation sweep. Tunnel-queue task —
	// probes each installed_charts row's live helm release and flags drift.
	TypeToolDriftSweep = tasks.ToolDriftSweepType
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

// TunnelQueueName is the dedicated asynq queue for tasks that require
// the tunnel hub (which only lives in the server pod). The standalone
// astronomer-worker pod does NOT subscribe to this queue.
const TunnelQueueName = tasks.ClusterTemplateApplyQueueName

// defaultTunnelWorkerConcurrency is the fallback when no positive value is
// configured (M11). Higher than the old hardcoded 2 so a couple of long helm
// installs no longer starve short tunnel RPCs.
const defaultTunnelWorkerConcurrency = 8

// NewTunnelWorker creates an Asynq server that exclusively drains the
// "tunnel" queue. It is started inside the server pod's process because
// the cluster_template:apply task (and its drift sweep) call into the
// tunnel-bound ToolHandler.EnsureInstalled — that path is unreachable
// from the standalone worker pod, which has no WebSocket terminations.
// Concurrency is configurable (M11): it was hardcoded to 2, so two long-lived
// apply runs (helm install of multiple operators, up to ~10m each) starved every
// short tunnel RPC across the platform. A non-positive value falls back to
// defaultTunnelWorkerConcurrency.
func NewTunnelWorker(redisURL string, concurrency int, log *slog.Logger) (*Worker, error) {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}
	if concurrency <= 0 {
		concurrency = defaultTunnelWorkerConcurrency
	}
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			TunnelQueueName: 1,
		},
	})
	return &Worker{
		server: srv,
		mux:    asynq.NewServeMux(),
		log:    log,
	}, nil
}

// RegisterTunnelHandlers wires the tunnel-only handler set on the mux.
// Kept separate from RegisterHandlers so the apply task isn't double-
// registered on the standalone worker pod (where it'd just short-circuit
// with "runtime not configured" and waste a redis round-trip).
func (w *Worker) RegisterTunnelHandlers() {
	w.mux.HandleFunc(TypeClusterTemplateApply, instrumentTask(TypeClusterTemplateApply, tasks.HandleClusterTemplateApply))
	w.mux.HandleFunc(TypeClusterTemplateDriftCheck, instrumentTask(TypeClusterTemplateDriftCheck, tasks.HandleClusterTemplateDriftCheck))
	w.mux.HandleFunc(tasks.MeshDetectType, instrumentTask(tasks.MeshDetectType, tasks.HandleMeshDetect))
	w.mux.HandleFunc(tasks.ClusterGroupMetricsRefreshType, instrumentTask(tasks.ClusterGroupMetricsRefreshType, tasks.HandleClusterGroupMetricsRefresh))
	w.mux.HandleFunc(tasks.GatekeeperPolicyApplyType, instrumentTask(tasks.GatekeeperPolicyApplyType, tasks.HandleGatekeeperPolicyApply))
	w.mux.HandleFunc(TypeToolDriftSweep, instrumentTask(TypeToolDriftSweep, tasks.HandleToolDriftSweep))
	// Decommission (individual + periodic sweep) runs here, not on the
	// standalone worker, so the managed-side cleanup phase can reach a
	// connected agent via the hub.
	w.mux.HandleFunc(TypeClusterDecommission, instrumentTask(TypeClusterDecommission, tasks.HandleClusterDecommission))
	w.mux.HandleFunc(TypeClusterDecommissionAll, instrumentTask(TypeClusterDecommissionAll, tasks.HandleClusterDecommissionAll))
	w.log.Info("registered tunnel-queue task handlers")
}

// RegisterHandlers sets up all task handlers on the mux.
func (w *Worker) RegisterHandlers() {
	w.mux.HandleFunc(TypeHealthCheck, instrumentTask(TypeHealthCheck, tasks.HandleHealthCheck))
	// Sprint 086 — cluster-condition remediation reconciler.
	w.mux.HandleFunc(tasks.ClusterConditionReconcileType, instrumentTask(tasks.ClusterConditionReconcileType, tasks.HandleClusterConditionReconcile))
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
	w.mux.HandleFunc(tasks.ApiserverAuditRetentionType, instrumentTask(tasks.ApiserverAuditRetentionType, tasks.HandleApiserverAuditRetention))
	w.mux.HandleFunc(TypeRunScheduledBackups, instrumentTask(TypeRunScheduledBackups, tasks.HandleRunScheduledBackups))
	w.mux.HandleFunc(TypeEnforceBackupRetention, instrumentTask(TypeEnforceBackupRetention, tasks.HandleEnforceBackupRetention))
	w.mux.HandleFunc(TypeRunRestore, instrumentTask(TypeRunRestore, tasks.HandleRunRestore))
	w.mux.HandleFunc(TypeProjectReconcile, instrumentTask(TypeProjectReconcile, tasks.HandleProjectReconcile))
	w.mux.HandleFunc(TypeProjectReconcileAll, instrumentTask(TypeProjectReconcileAll, tasks.HandleProjectReconcileAll))
	// NOTE: cluster:decommission and cluster:decommission_all are NOT registered
	// on the standalone worker. They need the WS tunnel hub (managed-side agent
	// uninstall), which lives only in the server pod — so both run on the
	// server's tunnel-queue worker (see RegisterTunnelHandlers) and are
	// enqueued/scheduled to the "tunnel" queue.
	w.mux.HandleFunc(TypeArgoCDRefreshManagedClusterLabels, instrumentTask(TypeArgoCDRefreshManagedClusterLabels, tasks.HandleArgoCDRefreshManagedClusterLabels))
	w.mux.HandleFunc(TypeArgoCDAutoRegisterCluster, instrumentTask(TypeArgoCDAutoRegisterCluster, tasks.HandleArgoCDAutoRegisterCluster))
	w.mux.HandleFunc(tasks.RefreshGroupSyncMetricsType, instrumentTask(tasks.RefreshGroupSyncMetricsType, tasks.HandleRefreshGroupSyncMetrics))
	w.mux.HandleFunc(TypeTelemetrySend, instrumentTask(TypeTelemetrySend, tasks.HandleTelemetrySend))
	w.mux.HandleFunc(TypeEmailDispatch, instrumentTask(TypeEmailDispatch, tasks.HandleEmailDispatch))
	w.mux.HandleFunc(TypeEmailCleanupOld, instrumentTask(TypeEmailCleanupOld, tasks.HandleEmailCleanupOld))
	w.mux.HandleFunc(TypeWebhookDispatch, instrumentTask(TypeWebhookDispatch, tasks.HandleWebhookDispatch))
	w.mux.HandleFunc(TypeWebhookCleanupOld, instrumentTask(TypeWebhookCleanupOld, tasks.HandleWebhookCleanupOld))
	// cluster_template:apply + drift_check are tunnel-only — registered
	// on the server-embedded tunnel worker, not here. See RegisterTunnelHandlers.
	w.mux.HandleFunc(TypeClusterApplyRegistrySecret, instrumentTask(TypeClusterApplyRegistrySecret, tasks.HandleClusterApplyRegistrySecret))
	w.mux.HandleFunc(TypeClusterRegistryDriftReconcile, instrumentTask(TypeClusterRegistryDriftReconcile, tasks.HandleClusterRegistryDriftReconcile))
	w.mux.HandleFunc(TypeClusterSnapshotPoll, instrumentTask(TypeClusterSnapshotPoll, tasks.HandleClusterSnapshotPoll))
	w.mux.HandleFunc(TypeClusterSnapshotDispatchScheduled, instrumentTask(TypeClusterSnapshotDispatchScheduled, tasks.HandleClusterSnapshotDispatchScheduled))
	w.mux.HandleFunc(TypeClusterSnapshotCleanupExpired, instrumentTask(TypeClusterSnapshotCleanupExpired, tasks.HandleClusterSnapshotCleanupExpired))
	w.mux.HandleFunc(tasks.ControlPlaneSnapshotSweepType, instrumentTask(tasks.ControlPlaneSnapshotSweepType, tasks.HandleControlPlaneSnapshotSweep))
	w.mux.HandleFunc(TypeCloudCredentialMaterialize, instrumentTask(TypeCloudCredentialMaterialize, tasks.HandleCloudCredentialMaterialize))
	w.mux.HandleFunc(TypeCloudCredentialDriftReconcile, instrumentTask(TypeCloudCredentialDriftReconcile, tasks.HandleCloudCredentialDriftReconcile))
	w.mux.HandleFunc(TypePlaintextCredentialMigration, instrumentTask(TypePlaintextCredentialMigration, tasks.HandlePlaintextCredentialMigration))
	w.mux.HandleFunc(TypeSIEMDispatch, instrumentTask(TypeSIEMDispatch, tasks.HandleSIEMDispatch))
	w.mux.HandleFunc(TypeSIEMCleanupOld, instrumentTask(TypeSIEMCleanupOld, tasks.HandleSIEMCleanupOld))
	w.mux.HandleFunc(TypeFleetOrchestrate, instrumentTask(TypeFleetOrchestrate, tasks.HandleFleetOrchestrate))
	// Durable agent-token rotation policy sweep (task A2). DB-only —
	// flags clusters whose token_rotation_days policy elapsed; the tunnel
	// server drives the grace rotation on the agent's next connect.
	w.mux.HandleFunc(tasks.AgentTokenRotateSweepType, instrumentTask(tasks.AgentTokenRotateSweepType, tasks.HandleAgentTokenRotateSweep))
	w.mux.HandleFunc(TypeDispatchDeferred, instrumentTask(TypeDispatchDeferred, tasks.HandleDispatchDeferred))
	w.mux.HandleFunc(TypeTaskOutboxDispatch, instrumentTask(TypeTaskOutboxDispatch, tasks.HandleTaskOutboxDispatch))
	// Migration 060: GitOps cluster registration sync.
	w.mux.HandleFunc(tasks.GitOpsSyncType, instrumentTask(tasks.GitOpsSyncType, tasks.HandleGitOpsSync))
	w.mux.HandleFunc(TypeKubectlSessionReap, instrumentTask(TypeKubectlSessionReap, tasks.HandleKubectlSessionReap))
	// Migration 068: NetworkPolicy template reconciler + drift sweep.
	w.mux.HandleFunc(TypeNetworkPolicyApply, instrumentTask(TypeNetworkPolicyApply, tasks.HandleNetworkPolicyApply))
	w.mux.HandleFunc(TypeNetworkPolicyDriftCheck, instrumentTask(TypeNetworkPolicyDriftCheck, tasks.HandleNetworkPolicyDriftCheck))
	w.mux.HandleFunc(TypeCrdMirrorPruneStale, instrumentTask(TypeCrdMirrorPruneStale, tasks.HandleCrdMirrorPruneStale))
	w.mux.HandleFunc(TypeCrdMirrorGaugePopulate, instrumentTask(TypeCrdMirrorGaugePopulate, tasks.HandleCrdMirrorGaugePopulate))
	w.mux.HandleFunc(TypeCRDOwnershipDriftCheck, instrumentTask(TypeCRDOwnershipDriftCheck, tasks.HandleCRDOwnershipDriftCheck))
	// Migration 070: apiserver allow-list reconciler.
	w.mux.HandleFunc(TypeApiserverAllowlistReconcile, instrumentTask(TypeApiserverAllowlistReconcile, tasks.HandleApiserverAllowlistReconcile))
	w.mux.HandleFunc(TypeApiserverAllowlistReconcileAll, instrumentTask(TypeApiserverAllowlistReconcileAll, tasks.HandleApiserverAllowlistReconcileAll))
	w.mux.HandleFunc(TypeApiserverAllowlistCleanupSnapshots, instrumentTask(TypeApiserverAllowlistCleanupSnapshots, tasks.HandleApiserverAllowlistCleanupSnapshots))
	w.mux.HandleFunc(TypeAnomalyBaselineRecompute, instrumentTask(TypeAnomalyBaselineRecompute, tasks.HandleAnomalyBaselineRecompute))
	w.mux.HandleFunc(TypeXClusterAnomalyRecompute, instrumentTask(TypeXClusterAnomalyRecompute, tasks.HandleXClusterAnomalyRecompute))
	w.mux.HandleFunc(TypeChartRecommendationsRecompute, instrumentTask(TypeChartRecommendationsRecompute, tasks.HandleChartRecommendationsRecompute))

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
