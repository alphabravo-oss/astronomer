package worker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type TaskOwner string
type TaskCapability string

const (
	TaskOwnerWorker TaskOwner = "worker"
	TaskOwnerTunnel TaskOwner = "server_tunnel"

	CapabilityDatabase      TaskCapability = "database"
	CapabilityRedis         TaskCapability = "redis"
	CapabilityTunnel        TaskCapability = "member_cluster_tunnel"
	CapabilityEncryption    TaskCapability = "platform_encryption"
	CapabilityDelivery      TaskCapability = "delivery_runtime"
	CapabilityOutboundHTTP  TaskCapability = "outbound_http"
	CapabilityProductBridge TaskCapability = "charlie_product_bridge"
)

type TaskSchedule struct {
	Cron        string `json:"cron"`
	Description string `json:"description"`
}

// TaskDescriptor is the executable ownership contract for one task type.
// Handler registration consumes this registry, preventing a task from being
// accidentally added to both process types.
type TaskDescriptor struct {
	Type                 string            `json:"type"`
	Queue                string            `json:"queue"`
	Owner                TaskOwner         `json:"owner"`
	RequiredCapabilities []TaskCapability  `json:"required_capabilities"`
	RetryClass           string            `json:"retry_class"`
	IdempotencyClass     string            `json:"idempotency_class"`
	TerminalState        string            `json:"terminal_state"`
	Recovery             string            `json:"recovery"`
	OptionalFeature      string            `json:"optional_feature,omitempty"`
	RuntimeBound         bool              `json:"runtime_bound,omitempty"`
	Schedules            []TaskSchedule    `json:"schedules,omitempty"`
	Handler              asynq.HandlerFunc `json:"-"`
}

func optionalDescriptor(feature string, item TaskDescriptor) TaskDescriptor {
	item.OptionalFeature = feature
	return item
}

func runtimeBoundDescriptor(item TaskDescriptor) TaskDescriptor {
	item.RuntimeBound = true
	return item
}

func rejectUnboundRuntimeTask(context.Context, *asynq.Task) error {
	return fmt.Errorf("task handler requires an explicit runtime binding")
}

func descriptor(owner TaskOwner, taskType string, handler asynq.HandlerFunc, required ...TaskCapability) TaskDescriptor {
	queue := "default"
	if owner == TaskOwnerTunnel {
		queue = TunnelQueueName
	}
	base := []TaskCapability{CapabilityRedis, CapabilityDatabase}
	base = append(base, required...)
	return TaskDescriptor{
		Type: taskType, Queue: queue, Owner: owner, Handler: handler,
		RuntimeBound:         true,
		RequiredCapabilities: base,
		RetryClass:           "bounded_exponential", IdempotencyClass: "reconcile_or_claimed_row",
		TerminalState: "durable status or terminal failure", Recovery: "periodic sweep or durable task outbox",
	}
}

var taskDescriptors = []TaskDescriptor{
	descriptor(TaskOwnerWorker, TypeHealthCheck, tasks.HandleHealthCheck),
	descriptor(TaskOwnerWorker, tasks.ClusterConditionReconcileType, tasks.HandleClusterConditionReconcile),
	descriptor(TaskOwnerWorker, TypeAlertEvaluation, tasks.HandleAlertEvaluation),
	descriptor(TaskOwnerWorker, TypeCatalogSync, tasks.HandleCatalogSync, CapabilityOutboundHTTP, CapabilityEncryption),
	descriptor(TaskOwnerWorker, TypeMetricsAggregation, tasks.HandleMetricsAggregation),
	descriptor(TaskOwnerWorker, TypeMonitoringReconcile, tasks.HandleMonitoringReconcile, CapabilityOutboundHTTP, CapabilityEncryption),
	descriptor(TaskOwnerWorker, TypeBackupExecution, tasks.HandleBackupExecution),
	// Compatibility consumers drain tasks queued by pre-Velero releases. These
	// types are intentionally not scheduled anymore: Velero owns cron and TTL,
	// while the server-side reconciler owns count-based retention.
	descriptor(TaskOwnerWorker, TypeRunScheduledBackups, tasks.HandleRunScheduledBackups),
	descriptor(TaskOwnerWorker, TypeEnforceBackupRetention, tasks.HandleEnforceBackupRetention),
	descriptor(TaskOwnerWorker, TypeSecurityScan, tasks.HandleSecurityScan),
	descriptor(TaskOwnerWorker, TypeNotificationSend, tasks.HandleNotificationSend),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeCharlieAlertDispatch, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeCharlieAlertReconcile, rejectUnboundRuntimeTask)),
	descriptor(TaskOwnerWorker, TypeAgentManifest, tasks.HandleAgentManifest),
	descriptor(TaskOwnerWorker, TypeCleanupExpiredRegistrationTokens, tasks.HandleCleanupRegistrationTokens),
	descriptor(TaskOwnerWorker, TypeCleanupOldAlertEvents, tasks.HandleCleanupAlertEvents),
	descriptor(TaskOwnerWorker, TypeEnsureAuditLogPartitions, tasks.HandleEnsureAuditLogPartitions),
	descriptor(TaskOwnerWorker, TypeEnforceAuditLogRetention, tasks.HandleEnforceAuditLogRetention),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeAuditOutboxDispatch, rejectUnboundRuntimeTask)),
	descriptor(TaskOwnerWorker, tasks.ApiserverAuditRetentionType, tasks.HandleApiserverAuditRetention),
	descriptor(TaskOwnerWorker, tasks.ClusterTombstoneRetentionType, tasks.HandleClusterTombstoneRetention),
	optionalDescriptor("managementBackup.enabled", descriptor(TaskOwnerWorker, tasks.ManagementBackupReconcileType, tasks.HandleManagementBackupReconcile)),
	optionalDescriptor("managementBackup.enabled", descriptor(TaskOwnerWorker, tasks.ManagementBackupOperationType, tasks.HandleManagementBackupOperation)),
	descriptor(TaskOwnerWorker, TypeRunRestore, tasks.HandleRunRestore),
	descriptor(TaskOwnerWorker, tasks.RefreshGroupSyncMetricsType, tasks.HandleRefreshGroupSyncMetrics),
	descriptor(TaskOwnerWorker, TypeTelemetrySend, tasks.HandleTelemetrySend, CapabilityOutboundHTTP),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeEmailDispatch, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeEmailCleanupOld, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeWebhookDispatch, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeWebhookCleanupOld, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypePlaintextCredentialMigration, rejectUnboundRuntimeTask, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeSIEMDispatch, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeSIEMCleanupOld, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, tasks.AgentTokenRotateSweepType, rejectUnboundRuntimeTask)),
	descriptor(TaskOwnerWorker, tasks.AgentUpgradeStuckSweepType, tasks.HandleAgentUpgradeStuckSweep),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeDispatchDeferred, rejectUnboundRuntimeTask, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeTaskOutboxDispatch, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeAdminQueueOperation, rejectUnboundRuntimeTask)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, tasks.DeliveryRolloutReconcileType, rejectUnboundRuntimeTask, CapabilityDelivery)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, tasks.DeliverySourceResolutionType, rejectUnboundRuntimeTask, CapabilityDelivery, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, tasks.DeliverySystemRolloutReconcileType, rejectUnboundRuntimeTask, CapabilityDelivery)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, tasks.GitOpsSyncType, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	descriptor(TaskOwnerWorker, TypeCrdMirrorPruneStale, tasks.HandleCrdMirrorPruneStale),
	descriptor(TaskOwnerWorker, TypeCrdMirrorGaugePopulate, tasks.HandleCrdMirrorGaugePopulate),
	optionalDescriptor("crds.enabled", runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeCRDOwnershipDriftCheck, rejectUnboundRuntimeTask, CapabilityTunnel))),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeApiserverAllowlistReconcile, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeApiserverAllowlistReconcileAll, rejectUnboundRuntimeTask, CapabilityEncryption, CapabilityOutboundHTTP)),
	runtimeBoundDescriptor(descriptor(TaskOwnerWorker, TypeApiserverAllowlistCleanupSnapshots, rejectUnboundRuntimeTask)),
	descriptor(TaskOwnerWorker, TypeAnomalyBaselineRecompute, tasks.HandleAnomalyBaselineRecompute),
	descriptor(TaskOwnerWorker, TypeXClusterAnomalyRecompute, tasks.HandleXClusterAnomalyRecompute),
	descriptor(TaskOwnerWorker, TypeChartRecommendationsRecompute, tasks.HandleChartRecommendationsRecompute),

	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeSecurityIngest, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeSecurityIngestRecovery, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeProjectReconcile, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeProjectReconcileAll, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterTemplateApply, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterTemplateDriftCheck, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterApplyRegistrySecret, rejectUnboundRuntimeTask, CapabilityTunnel, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterRegistryDriftReconcile, rejectUnboundRuntimeTask, CapabilityTunnel, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterSnapshotPoll, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterSnapshotDispatchScheduled, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterSnapshotCleanupExpired, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterSnapshotApplyOperation, rejectUnboundRuntimeTask, CapabilityTunnel)),
	optionalDescriptor("controlPlaneSnapshots.enabled", runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, tasks.ControlPlaneSnapshotSweepType, rejectUnboundRuntimeTask, CapabilityTunnel))),
	optionalDescriptor("controlPlaneSnapshots.enabled", runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, tasks.ControlPlaneSnapshotApplyType, rejectUnboundRuntimeTask, CapabilityTunnel))),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeCloudCredentialMaterialize, rejectUnboundRuntimeTask, CapabilityTunnel, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeCloudCredentialDriftReconcile, rejectUnboundRuntimeTask, CapabilityTunnel, CapabilityEncryption)),
	optionalDescriptor("kubectlShell.enabled", runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeKubectlSessionReap, rejectUnboundRuntimeTask, CapabilityTunnel))),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeNetworkPolicyApply, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeNetworkPolicyDriftCheck, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, tasks.MeshDetectType, rejectUnboundRuntimeTask, CapabilityTunnel)),
	descriptor(TaskOwnerTunnel, tasks.ClusterGroupMetricsRefreshType, tasks.HandleClusterGroupMetricsRefresh, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.GatekeeperPolicyApplyType, tasks.HandleGatekeeperPolicyApply, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.GatekeeperConstraintReconcileType, tasks.HandleGatekeeperConstraintReconcile, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.GatekeeperConstraintReconcileAllType, tasks.HandleGatekeeperConstraintReconcileAll, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.ImageVulnerabilityRescanType, tasks.HandleImageVulnerabilityRescan, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.PodDeleteType, tasks.HandlePodDelete, CapabilityTunnel),
	descriptor(TaskOwnerTunnel, tasks.ResourceOperationType, tasks.HandleResourceOperation, CapabilityTunnel, CapabilityEncryption),
	descriptor(TaskOwnerTunnel, tasks.NodeOperationType, tasks.HandleNodeOperation, CapabilityTunnel, CapabilityEncryption),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeToolDriftSweep, rejectUnboundRuntimeTask, CapabilityTunnel)),
	optionalDescriptor("feature.charlie", runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeCharlieTriggerDispatch, rejectUnboundRuntimeTask, CapabilityProductBridge))),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterDecommission, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, TypeClusterDecommissionAll, rejectUnboundRuntimeTask, CapabilityTunnel)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, tasks.DexOperationType, rejectUnboundRuntimeTask, CapabilityTunnel, CapabilityEncryption)),
	runtimeBoundDescriptor(descriptor(TaskOwnerTunnel, tasks.DexOperationRecoveryType, rejectUnboundRuntimeTask, CapabilityTunnel)),
}

type ScheduledTaskSpec struct {
	TaskType    string
	Owner       TaskOwner
	Queue       string
	Cron        string
	Description string
}

func scheduled(owner TaskOwner, taskType, cron, description string) ScheduledTaskSpec {
	queue := "default"
	if owner == TaskOwnerTunnel {
		queue = TunnelQueueName
	}
	return ScheduledTaskSpec{TaskType: taskType, Owner: owner, Queue: queue, Cron: cron, Description: description}
}

var scheduledTaskSpecs = []ScheduledTaskSpec{
	scheduled(TaskOwnerTunnel, tasks.DexOperationRecoveryType, "@every 2m", "recover durable Dex operations after queue loss"),
	scheduled(TaskOwnerWorker, TypeHealthCheck, "@every 60s", "cluster health check"),
	scheduled(TaskOwnerWorker, tasks.ClusterConditionReconcileType, "@every 30s", "cluster-condition remediation"),
	scheduled(TaskOwnerWorker, TypeAlertEvaluation, "@every 60s", "alert rule evaluation"),
	scheduled(TaskOwnerWorker, TypeCharlieAlertReconcile, "@every 60s", "Charlie alert delivery reconciliation"),
	scheduled(TaskOwnerWorker, tasks.DeliveryRolloutReconcileType, "@every 5s", "Flux-native delivery rollout reconciliation"),
	scheduled(TaskOwnerWorker, tasks.DeliverySourceResolutionType, "@every 15s", "immutable delivery source resolution sweep"),
	scheduled(TaskOwnerWorker, tasks.DeliverySystemRolloutReconcileType, "@every 5s", "signed agent and Flux system rollout reconciliation"),
	scheduled(TaskOwnerWorker, TypeCatalogSync, "@every 6h", "catalog sync"),
	scheduled(TaskOwnerWorker, TypeMetricsAggregation, "@every 5m", "metrics aggregation"),
	scheduled(TaskOwnerWorker, TypeMonitoringReconcile, "@every 2m", "monitoring reconciliation"),
	scheduled(TaskOwnerWorker, TypeCleanupExpiredRegistrationTokens, "@every 6h", "cleanup expired registration tokens"),
	scheduled(TaskOwnerWorker, TypeCleanupOldAlertEvents, "0 2 * * *", "cleanup old alert events"),
	scheduled(TaskOwnerWorker, TypeEnsureAuditLogPartitions, "0 1 * * *", "ensure audit-log partitions"),
	scheduled(TaskOwnerWorker, TypeEnforceAuditLogRetention, "30 1 * * *", "enforce audit-log retention"),
	scheduled(TaskOwnerWorker, TypeAuditOutboxDispatch, "@every 2s", "durable audit-intent dispatch"),
	scheduled(TaskOwnerWorker, tasks.ApiserverAuditRetentionType, "45 1 * * *", "enforce API-server audit retention"),
	scheduled(TaskOwnerWorker, TypeChartRecommendationsRecompute, "30 3 * * *", "chart recommendations recompute"),
	scheduled(TaskOwnerWorker, tasks.RefreshGroupSyncMetricsType, "@every 5m", "refresh group-sync binding gauge"),
	scheduled(TaskOwnerWorker, tasks.AgentUpgradeStuckSweepType, "@every 5m", "fail stuck agent upgrades"),
	scheduled(TaskOwnerWorker, TypeTelemetrySend, "30 2 * * *", "telemetry send"),
	scheduled(TaskOwnerWorker, TypeEmailDispatch, "@every 30s", "email dispatch"),
	scheduled(TaskOwnerWorker, TypeEmailCleanupOld, "30 3 * * *", "email retention"),
	scheduled(TaskOwnerWorker, TypeWebhookDispatch, "@every 15s", "webhook dispatch"),
	scheduled(TaskOwnerWorker, TypeWebhookCleanupOld, "0 4 * * *", "webhook delivery retention"),
	scheduled(TaskOwnerWorker, tasks.ClusterTombstoneRetentionType, "0 5 * * *", "cluster tombstone retention"),
	scheduled(TaskOwnerWorker, TypePlaintextCredentialMigration, "@every 6h", "plaintext credential migration"),
	scheduled(TaskOwnerWorker, TypeSIEMDispatch, "@every 2s", "SIEM dispatch"),
	scheduled(TaskOwnerWorker, TypeSIEMCleanupOld, "30 4 * * *", "SIEM queue retention"),
	scheduled(TaskOwnerWorker, tasks.AgentTokenRotateSweepType, "@every 1h", "agent-token rotation policy"),
	scheduled(TaskOwnerTunnel, TypeDispatchDeferred, "@every 60s", "maintenance deferred-operation dispatch"),
	scheduled(TaskOwnerTunnel, TypeSecurityIngestRecovery, "@every 1m", "CIS scan ingestion recovery"),
	scheduled(TaskOwnerWorker, TypeTaskOutboxDispatch, "@every 15s", "task outbox dispatch"),
	scheduled(TaskOwnerWorker, tasks.GitOpsSyncType, "@every 60s", "GitOps cluster registration sync"),
	scheduled(TaskOwnerWorker, TypeCrdMirrorPruneStale, "@every 30m", "CRD mirror stale-row prune"),
	scheduled(TaskOwnerWorker, TypeCrdMirrorGaugePopulate, "@every 1m", "CRD mirror gauge"),
	scheduled(TaskOwnerTunnel, TypeCRDOwnershipDriftCheck, "@every 5m", "CRD ownership drift"),
	scheduled(TaskOwnerWorker, TypeAnomalyBaselineRecompute, "@every 5m", "anomaly baseline recompute"),
	scheduled(TaskOwnerWorker, TypeXClusterAnomalyRecompute, "@every 5m", "cross-cluster anomaly recompute"),
	scheduled(TaskOwnerWorker, TypeApiserverAllowlistReconcileAll, "@every 15m", "API-server allow-list reconcile"),
	scheduled(TaskOwnerWorker, TypeApiserverAllowlistCleanupSnapshots, "45 4 * * *", "API-server allow-list snapshot retention"),

	scheduled(TaskOwnerTunnel, TypeProjectReconcileAll, "@every 5m", "project enforcement sweep"),
	scheduled(TaskOwnerTunnel, TypeClusterRegistryDriftReconcile, "@every 30m", "cluster registry drift"),
	scheduled(TaskOwnerTunnel, TypeClusterSnapshotPoll, "@every 30s", "cluster snapshot poll"),
	scheduled(TaskOwnerTunnel, TypeClusterSnapshotDispatchScheduled, "@every 1m", "cluster snapshot schedule dispatch"),
	scheduled(TaskOwnerTunnel, TypeClusterSnapshotCleanupExpired, "15 4 * * *", "cluster snapshot retention"),
	scheduled(TaskOwnerTunnel, tasks.ControlPlaneSnapshotSweepType, "@every 1m", "control-plane snapshot sweep"),
	scheduled(TaskOwnerTunnel, TypeCloudCredentialDriftReconcile, "@every 30m", "cloud credential drift"),
	scheduled(TaskOwnerTunnel, TypeKubectlSessionReap, "@every 60s", "kubectl session reaper"),
	scheduled(TaskOwnerTunnel, TypeNetworkPolicyApply, "@every 5m", "network policy apply"),
	scheduled(TaskOwnerTunnel, TypeNetworkPolicyDriftCheck, "@every 30m", "network policy drift"),
	scheduled(TaskOwnerTunnel, TypeClusterTemplateDriftCheck, "@every 1h", "cluster template drift"),
	scheduled(TaskOwnerTunnel, TypeClusterDecommissionAll, "@every 1m", "cluster decommission sweep"),
	scheduled(TaskOwnerTunnel, tasks.MeshDetectType, "@every 5m", "service mesh detection"),
	scheduled(TaskOwnerTunnel, tasks.GatekeeperPolicyApplyType, "@every 5m", "Gatekeeper policy apply"),
	scheduled(TaskOwnerTunnel, tasks.GatekeeperConstraintReconcileAllType, "@every 1m", "authored Gatekeeper constraint reconciliation"),
	scheduled(TaskOwnerTunnel, tasks.ClusterGroupMetricsRefreshType, "@every 5m", "cluster group metrics"),
	scheduled(TaskOwnerTunnel, TypeToolDriftSweep, "@every 1h", "tool drift sweep"),
}

func ScheduledTaskSpecs() []ScheduledTaskSpec {
	return append([]ScheduledTaskSpec(nil), scheduledTaskSpecs...)
}

func scheduledTaskSpecsForFeatures(features SchedulerFeatures) []ScheduledTaskSpec {
	out := make([]ScheduledTaskSpec, 0, len(scheduledTaskSpecs))
	for _, spec := range scheduledTaskSpecs {
		if spec.TaskType == TypeCRDOwnershipDriftCheck && !features.CRDOwnership {
			continue
		}
		out = append(out, spec)
	}
	return out
}

var taskRegistryDescriptorGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "astronomer",
	Name:      "task_registry_descriptors",
	Help:      "Number of registered task descriptors by owning process and queue.",
}, []string{"owner", "queue"})

func init() {
	prometheus.MustRegister(taskRegistryDescriptorGauge)
	for key, count := range TaskRegistryCounts() {
		parts := strings.SplitN(key, "/", 2)
		taskRegistryDescriptorGauge.WithLabelValues(parts[0], parts[1]).Set(float64(count))
	}
}

func TaskRegistryCounts() map[string]int {
	counts := map[string]int{}
	for _, item := range taskDescriptors {
		counts[string(item.Owner)+"/"+item.Queue]++
	}
	return counts
}

func TaskDescriptors() []TaskDescriptor {
	out := append([]TaskDescriptor(nil), taskDescriptors...)
	byType := make(map[string]int, len(out))
	for i := range out {
		byType[out[i].Type] = i
	}
	for _, spec := range scheduledTaskSpecs {
		if i, ok := byType[spec.TaskType]; ok {
			out[i].Schedules = append(out[i].Schedules, TaskSchedule{Cron: spec.Cron, Description: spec.Description})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

func descriptorsForOwner(owner TaskOwner) []TaskDescriptor {
	out := make([]TaskDescriptor, 0)
	for _, item := range taskDescriptors {
		if item.Owner == owner {
			out = append(out, item)
		}
	}
	return out
}

func descriptorsForStandalone(features tasks.StandaloneRuntimeFeatures) []TaskDescriptor {
	out := make([]TaskDescriptor, 0)
	for _, item := range descriptorsForOwner(TaskOwnerWorker) {
		if item.OptionalFeature == "managementBackup.enabled" && !features.ManagementBackup {
			continue
		}
		out = append(out, item)
	}
	return out
}

func descriptorsForTunnel(features tasks.TunnelRuntimeFeatures) []TaskDescriptor {
	out := make([]TaskDescriptor, 0)
	for _, item := range descriptorsForOwner(TaskOwnerTunnel) {
		if item.OptionalFeature == "crds.enabled" && !features.CRDOwnership {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolveTaskDescriptorSet(owner TaskOwner, resolved []TaskDescriptor, bindings map[string]asynq.HandlerFunc) ([]TaskDescriptor, error) {
	knownBindings := make(map[string]struct{})
	missing := make([]string, 0)
	for i := range resolved {
		if !resolved[i].RuntimeBound {
			continue
		}
		knownBindings[resolved[i].Type] = struct{}{}
		handler := bindings[resolved[i].Type]
		if handler == nil {
			missing = append(missing, resolved[i].Type)
			continue
		}
		resolved[i].Handler = handler
	}
	unknown := make([]string, 0)
	for taskType, handler := range bindings {
		if _, ok := knownBindings[taskType]; !ok || handler == nil {
			unknown = append(unknown, taskType)
		}
	}
	if len(missing) == 0 && len(unknown) == 0 {
		return resolved, nil
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	parts := make([]string, 0, 2)
	if len(missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		parts = append(parts, "unknown or nil: "+strings.Join(unknown, ", "))
	}
	return nil, fmt.Errorf("task runtime %s handler bindings are incomplete; %s", owner, strings.Join(parts, "; "))
}

func ValidateTaskRegistry() error {
	seen := map[string]TaskDescriptor{}
	for _, item := range taskDescriptors {
		if strings.TrimSpace(item.Type) == "" || item.Handler == nil || item.Queue == "" || item.Owner == "" {
			return fmt.Errorf("invalid task descriptor: %+v", item)
		}
		if previous, ok := seen[item.Type]; ok {
			return fmt.Errorf("task %q has multiple owners: %s and %s", item.Type, previous.Owner, item.Owner)
		}
		seen[item.Type] = item
	}
	for _, spec := range scheduledTaskSpecs {
		item, ok := seen[spec.TaskType]
		if !ok {
			return fmt.Errorf("scheduled task %q has no registered handler", spec.TaskType)
		}
		if item.Owner != spec.Owner || item.Queue != spec.Queue {
			return fmt.Errorf("scheduled task %q routes to %s/%s but handler belongs to %s/%s", spec.TaskType, spec.Owner, spec.Queue, item.Owner, item.Queue)
		}
	}
	return nil
}

func ValidateTaskCapabilities(owner TaskOwner, available map[TaskCapability]bool) error {
	if err := ValidateTaskRegistry(); err != nil {
		return err
	}
	missing := make([]string, 0)
	for _, item := range descriptorsForOwner(owner) {
		// Optional handlers remain registered so their lifecycle can activate
		// without rebuilding the mux. The lifecycle itself validates its dynamic
		// dependencies; process boot validates every always-on task here.
		if item.OptionalFeature != "" {
			continue
		}
		for _, capability := range item.RequiredCapabilities {
			if !available[capability] {
				missing = append(missing, item.Type+":"+string(capability))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("task runtime %s is missing required capabilities: %s", owner, strings.Join(missing, ", "))
	}
	return nil
}
