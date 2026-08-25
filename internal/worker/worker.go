package worker

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

const managementBackupObservationRetryDelay = 15 * time.Second

func retryDelay(n int, err error, task *asynq.Task) time.Duration {
	if task != nil && task.Type() == tasks.ManagementBackupOperationType && err != nil && strings.Contains(err.Error(), "backup_in_progress") {
		return managementBackupObservationRetryDelay
	}
	return asynq.DefaultRetryDelayFunc(n, err, task)
}

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
	TypeSecurityIngestRecovery           = tasks.SecurityIngestRecoveryType
	TypeNotificationSend                 = "notification:send"
	TypeAgentManifest                    = "agent:generate_manifest"
	TypeCleanupExpiredRegistrationTokens = tasks.CleanupExpiredRegistrationTokensType
	TypeCleanupOldAlertEvents            = tasks.CleanupOldAlertEventsType
	TypeEnsureAuditLogPartitions         = tasks.EnsureAuditLogPartitionsType
	TypeEnforceAuditLogRetention         = tasks.EnforceAuditLogRetentionType
	TypeAuditOutboxDispatch              = tasks.AuditOutboxDispatchType
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
	// types share one immutable ClusterSnapshotRuntime.
	TypeClusterSnapshotPoll              = tasks.ClusterSnapshotPollType
	TypeClusterSnapshotDispatchScheduled = tasks.ClusterSnapshotDispatchScheduledType
	TypeClusterSnapshotCleanupExpired    = tasks.ClusterSnapshotCleanupExpiredType
	TypeClusterSnapshotApplyOperation    = tasks.ClusterSnapshotApplyOperationType
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
	// Migration 057: maintenance window deferred-op dispatcher.
	TypeDispatchDeferred = tasks.DispatchDeferredType
	// Migration 092: durable Postgres task outbox dispatcher. This is the
	// retry bridge from committed DB task intents into Redis/Asynq.
	TypeTaskOutboxDispatch     = tasks.TaskOutboxDispatchType
	TypeAdminQueueOperation    = tasks.AdminQueueOperationType
	TypeCharlieTriggerDispatch = tasks.CharlieTriggerDispatchType
	TypeCharlieAlertDispatch   = tasks.CharlieAlertDispatchTaskType
	TypeCharlieAlertReconcile  = tasks.CharlieAlertReconcileType
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
	// share one immutable ApiserverAllowlistRuntime.
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
	// Durable, tunnel-owned deletion/recreation of Trivy VulnerabilityReport
	// objects for one committed workload operation.
	TypeImageVulnerabilityRescan = tasks.ImageVulnerabilityRescanType
	TypePodDelete                = tasks.PodDeleteType
	// P1 item 16/22: tool drift reconciliation sweep. Tunnel-queue task —
	// probes each installed_charts row's live helm release and flags drift.
	TypeToolDriftSweep = tasks.ToolDriftSweepType
)

// Worker wraps the Asynq server for processing background tasks.
type Worker struct {
	server      *asynq.Server
	mux         *asynq.ServeMux
	log         *slog.Logger
	descriptors []TaskDescriptor
}

// StandaloneRuntime is the explicit, immutable handler graph owned by the
// standalone worker process. It contains the complete composition graph; task
// packages expose no mutable startup configurators.
type StandaloneRuntime struct {
	Features    tasks.StandaloneRuntimeFeatures
	Core        tasks.CoreRuntime
	Delivery    tasks.DeliveryRuntime
	Dispatch    tasks.DispatchRuntime
	Alerts      tasks.CharlieAlertRuntime
	Maintenance tasks.MaintenanceRuntime
	Allowlists  tasks.ApiserverAllowlistRuntime
	GitOps      tasks.GitOpsRuntime
}

// TunnelRuntime is the explicit handler graph owned by the server-embedded
// worker that drains only tunnel-dependent tasks.
type TunnelRuntime struct {
	Features             tasks.TunnelRuntimeFeatures
	Core                 tasks.CoreRuntime
	ToolDrift            tasks.ToolDriftRuntime
	CharlieTrigger       *tasks.CharlieTriggerRuntime
	ClusterTemplate      tasks.ClusterTemplateRuntime
	NetworkPolicy        tasks.NetworkPolicyRuntime
	Mesh                 tasks.MeshRuntime
	CloudCredential      tasks.CloudCredentialRuntime
	ClusterRegistry      tasks.ClusterRegistryRuntime
	Project              tasks.ProjectRuntime
	ClusterSnapshot      tasks.ClusterSnapshotRuntime
	ClusterDecommission  tasks.ClusterDecommissionRuntime
	ControlPlaneSnapshot tasks.ControlPlaneSnapshotRuntime
	Deferred             tasks.DeferredRuntime
	KubectlSessionReap   tasks.KubectlSessionReapRuntime
	SecurityIngest       tasks.SecurityIngestRuntime
	CRDOwnership         tasks.CRDOwnershipRuntime
	Dex                  tasks.DexOperationRuntime
}

func (runtime TunnelRuntime) handlerBindings() (map[string]asynq.HandlerFunc, error) {
	bindings, err := runtime.ToolDrift.HandlerBindings()
	if err != nil {
		return nil, err
	}
	dexBindings, err := runtime.Dex.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range dexBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	triggerBindings, err := runtime.CharlieTrigger.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range triggerBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	templateBindings, err := runtime.ClusterTemplate.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range templateBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	networkPolicyBindings, err := runtime.NetworkPolicy.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range networkPolicyBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	meshBindings, err := runtime.Mesh.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range meshBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	cloudBindings, err := runtime.CloudCredential.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range cloudBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	registryBindings, err := runtime.ClusterRegistry.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range registryBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	projectBindings, err := runtime.Project.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range projectBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	snapshotBindings, err := runtime.ClusterSnapshot.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range snapshotBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	decommissionBindings, err := runtime.ClusterDecommission.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range decommissionBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	controlPlaneBindings, err := runtime.ControlPlaneSnapshot.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range controlPlaneBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	deferredBindings, err := runtime.Deferred.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range deferredBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	kubectlBindings, err := runtime.KubectlSessionReap.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range kubectlBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	securityBindings, err := runtime.SecurityIngest.HandlerBindings()
	if err != nil {
		return nil, err
	}
	for taskType, handler := range securityBindings {
		if _, exists := bindings[taskType]; exists {
			return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
		}
		bindings[taskType] = handler
	}
	if runtime.Features.CRDOwnership {
		crdOwnershipBindings, err := runtime.CRDOwnership.HandlerBindings()
		if err != nil {
			return nil, err
		}
		for taskType, handler := range crdOwnershipBindings {
			if _, exists := bindings[taskType]; exists {
				return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
			}
			bindings[taskType] = handler
		}
	}
	if err := runtime.Core.ValidateTunnel(); err != nil {
		return nil, fmt.Errorf("validate tunnel core runtime: %w", err)
	}
	return bindCoreRuntimeHandlersForDescriptors(runtime.Core, bindings, descriptorsForTunnel(runtime.Features)), nil
}

func (runtime StandaloneRuntime) handlerBindings() (map[string]asynq.HandlerFunc, error) {
	bindings := make(map[string]asynq.HandlerFunc)
	families := []func() (map[string]asynq.HandlerFunc, error){
		runtime.Delivery.HandlerBindings,
		runtime.Dispatch.HandlerBindings,
		runtime.Alerts.HandlerBindings,
		runtime.Maintenance.HandlerBindings,
		runtime.Allowlists.HandlerBindings,
		runtime.GitOps.HandlerBindings,
	}
	for _, build := range families {
		familyBindings, err := build()
		if err != nil {
			return nil, err
		}
		for taskType, handler := range familyBindings {
			if _, exists := bindings[taskType]; exists {
				return nil, fmt.Errorf("duplicate runtime handler binding for %q", taskType)
			}
			bindings[taskType] = handler
		}
	}
	if err := runtime.Core.ValidateStandalone(runtime.Features); err != nil {
		return nil, fmt.Errorf("validate standalone core runtime: %w", err)
	}
	return bindCoreRuntimeHandlersForDescriptors(runtime.Core, bindings, descriptorsForStandalone(runtime.Features)), nil
}

func bindCoreRuntimeHandlersForDescriptors(core tasks.CoreRuntime, bindings map[string]asynq.HandlerFunc, descriptors []TaskDescriptor) map[string]asynq.HandlerFunc {
	for _, descriptor := range descriptors {
		handler := bindings[descriptor.Type]
		if handler == nil {
			handler = descriptor.Handler
		}
		bindings[descriptor.Type] = core.BindHandler(handler)
	}
	return bindings
}

// NewWorker creates a new Asynq-based background worker.
//
// An invalid REDIS_URL is fail-fast: returns a non-nil error rather than
// silently falling back to localhost:6379. The previous fallback was a
// footgun in air-gapped or split-network production clusters — the worker
// would come up, fail every redis op invisibly, and take hours to
// diagnose. Now a bad URL surfaces at process start.
func NewWorker(redisURL string, log *slog.Logger, runtime StandaloneRuntime, errorHandlers ...asynq.ErrorHandler) (*Worker, error) {
	bindings, err := runtime.handlerBindings()
	if err != nil {
		return nil, fmt.Errorf("compose standalone worker runtime: %w", err)
	}
	descriptors, err := resolveTaskDescriptorSet(TaskOwnerWorker, descriptorsForStandalone(runtime.Features), bindings)
	if err != nil {
		return nil, fmt.Errorf("compose standalone worker handlers: %w", err)
	}
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}

	config := asynq.Config{
		Concurrency:    10,
		RetryDelayFunc: retryDelay,
		Queues: map[string]int{
			"critical": 6,
			"default":  3,
			"low":      1,
		},
	}
	if len(errorHandlers) > 0 {
		config.ErrorHandler = errorHandlers[0]
	}
	srv := asynq.NewServer(redisOpt, config)

	return &Worker{
		server:      srv,
		mux:         asynq.NewServeMux(),
		log:         log,
		descriptors: descriptors,
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
func NewTunnelWorker(redisURL string, concurrency int, log *slog.Logger, runtime TunnelRuntime, errorHandlers ...asynq.ErrorHandler) (*Worker, error) {
	redisOpt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL %q: %w", redisURL, err)
	}
	if concurrency <= 0 {
		concurrency = defaultTunnelWorkerConcurrency
	}
	config := asynq.Config{
		Concurrency:    concurrency,
		RetryDelayFunc: retryDelay,
		Queues: map[string]int{
			TunnelQueueName: 1,
		},
	}
	if len(errorHandlers) > 0 {
		config.ErrorHandler = errorHandlers[0]
	}
	srv := asynq.NewServer(redisOpt, config)
	bindings, err := runtime.handlerBindings()
	if err != nil {
		return nil, fmt.Errorf("compose tunnel worker runtime: %w", err)
	}
	descriptors, err := resolveTaskDescriptorSet(TaskOwnerTunnel, descriptorsForTunnel(runtime.Features), bindings)
	if err != nil {
		return nil, fmt.Errorf("compose tunnel worker handlers: %w", err)
	}
	return &Worker{
		server:      srv,
		mux:         asynq.NewServeMux(),
		log:         log,
		descriptors: descriptors,
	}, nil
}

// RegisterTunnelHandlers wires the tunnel-only handler set on the mux.
// Kept separate from RegisterHandlers so the apply task isn't double-
// registered on the standalone worker pod (where it'd just short-circuit
// with "runtime not configured" and waste a redis round-trip).
func (w *Worker) RegisterTunnelHandlers() {
	for _, item := range w.descriptors {
		w.mux.HandleFunc(item.Type, instrumentTask(item.Type, item.Handler))
	}
	w.log.Info("registered tunnel-queue task handlers", "count", len(w.descriptors))
}

// RegisterHandlers sets up all task handlers on the mux.
func (w *Worker) RegisterHandlers() {
	for _, item := range w.descriptors {
		w.mux.HandleFunc(item.Type, instrumentTask(item.Type, item.Handler))
	}
	w.log.Info("registered worker task handlers", "count", len(w.descriptors))
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
