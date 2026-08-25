package tasks

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// TunnelRuntimeFeatures describes optional tunnel-owned task families that
// are enabled by deployment configuration. Always-on task dependencies are
// validated regardless of these flags.
type TunnelRuntimeFeatures struct {
	KubectlShell          bool
	ControlPlaneSnapshots bool
	CharlieProductBridge  bool
	CRDOwnership          bool
}

type StandaloneRuntimeFeatures struct {
	ManagementBackup bool
}

type requiredRuntimeDependency struct {
	name  string
	value any
}

func (runtime CoreRuntime) ValidateStandalone(features StandaloneRuntimeFeatures) error {
	runtime = runtime.normalized()
	dependencies := []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "leader", value: runtime.Deps.Leader},
		{name: "enqueuer", value: runtime.Deps.Enqueuer},
		{name: "catalog_decryptor", value: runtime.Deps.CatalogDecryptor},
		{name: "monitoring_cipher", value: runtime.Deps.MonitoringCipher},
	}
	if features.ManagementBackup {
		dependencies = append(dependencies, requiredRuntimeDependency{name: "management_backup", value: managementBackupRuntimeDependency(runtime.Deps.ManagementBackup)})
	}
	return validateRequiredRuntimeDependencies("standalone core runtime", dependencies)
}

func (runtime CoreRuntime) ValidateTunnel() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("tunnel core runtime", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "leader", value: runtime.Deps.Leader},
		{name: "k8s_requester", value: runtime.Deps.K8s},
		{name: "resource_decryptor", value: runtime.Deps.ResourceDecryptor},
	})
}

// ValidateStandaloneRuntime verifies the actual dependency graph consumed by
// worker-owned handlers. Capability labels alone cannot prove that a concrete
// reconciler, sender, cipher, or dynamic Kubernetes client was wired.
func ValidateStandaloneRuntime(features StandaloneRuntimeFeatures, coreRuntime CoreRuntime, deliveryRuntime DeliveryRuntime, dispatchRuntime DispatchRuntime, alertRuntime CharlieAlertRuntime, maintenanceRuntime MaintenanceRuntime, allowlistRuntime ApiserverAllowlistRuntime, gitopsRuntime GitOpsRuntime) error {
	coreRuntime = coreRuntime.normalized()
	dispatchRuntime = dispatchRuntime.normalized()

	dependencies := []requiredRuntimeDependency{
		{name: "runtime.queries", value: coreRuntime.Deps.Queries},
		{name: "runtime.leader", value: coreRuntime.Deps.Leader},
		{name: "runtime.enqueuer", value: coreRuntime.Deps.Enqueuer},
		{name: "runtime.catalog_decryptor", value: coreRuntime.Deps.CatalogDecryptor},
		{name: "runtime.monitoring_cipher", value: coreRuntime.Deps.MonitoringCipher},
		{name: "apiserver_allowlist.queries", value: allowlistRuntime.Deps.Queries},
		{name: "apiserver_allowlist.registry", value: allowlistRuntime.Deps.Registry},
		{name: "apiserver_allowlist.cluster_shaper", value: allowlistRuntime.Deps.ClusterShaper},
		{name: "apiserver_allowlist.audit_writer", value: allowlistRuntime.Deps.AuditWriter},
		{name: "delivery.source_resolver", value: deliveryRuntime.SourceResolver},
		{name: "delivery.rollout_reconciler", value: deliveryRuntime.RolloutReconciler},
		{name: "delivery.system_rollout_reconciler", value: deliveryRuntime.SystemRolloutReconciler},
		{name: "plaintext_credentials.queries", value: maintenanceRuntime.PlaintextCredentials.Queries},
		{name: "plaintext_credentials.encryptor", value: maintenanceRuntime.PlaintextCredentials.Encryptor},
		{name: "agent_token_rotation.queries", value: maintenanceRuntime.AgentTokens.Queries},
		{name: "gitops.queries", value: gitopsRuntime.Deps.Queries},
		{name: "gitops.task_outbox", value: gitopsRuntime.Deps.TaskOutbox},
		{name: "gitops.decryptor", value: gitopsRuntime.Deps.Decryptor},
		{name: "email.queries", value: dispatchRuntime.Email.Queries},
		{name: "email.sender", value: dispatchRuntime.Email.Sender},
		{name: "email.settings_provider", value: dispatchRuntime.Email.Provider},
		{name: "webhook.queries", value: dispatchRuntime.Webhook.Queries},
		{name: "webhook.sender", value: dispatchRuntime.Webhook.Sender},
		{name: "webhook.encryptor", value: dispatchRuntime.Webhook.Encryptor},
		{name: "siem.queries", value: dispatchRuntime.SIEM.Queries},
		{name: "siem.encryptor", value: dispatchRuntime.SIEM.Encryptor},
		{name: "siem.http_client", value: dispatchRuntime.SIEM.HTTPClient},
		{name: "siem.transport_factory", value: dispatchRuntime.SIEM.TransportFactory},
		{name: "task_outbox.queries", value: dispatchRuntime.TaskOutbox.Queries},
		{name: "task_outbox.enqueuer", value: dispatchRuntime.TaskOutbox.Enqueuer},
		{name: "audit_outbox.queries", value: dispatchRuntime.AuditOutbox.Queries},
		{name: "charlie_alert.queries", value: alertRuntime.Queries},
		{name: "charlie_alert.write_fence", value: alertRuntime.WriteFence},
		{name: "charlie_alert.reconciler", value: alertRuntime.Reconciler},
	}
	if features.ManagementBackup {
		dependencies = append(dependencies, requiredRuntimeDependency{name: "runtime.management_backup", value: managementBackupRuntimeDependency(coreRuntime.Deps.ManagementBackup)})
	}
	return validateRequiredRuntimeDependencies("standalone worker", dependencies)
}

// ValidateTunnelRuntime verifies every always-on tunnel task and any enabled
// optional task family before the server starts consuming the tunnel queue.
func ValidateTunnelRuntime(features TunnelRuntimeFeatures, coreRuntime CoreRuntime, toolDriftRuntime ToolDriftRuntime, charlieTriggerRuntime *CharlieTriggerRuntime, clusterTemplateRuntime ClusterTemplateRuntime, networkPolicyRuntime NetworkPolicyRuntime, meshRuntime MeshRuntime, cloudCredentialRuntime CloudCredentialRuntime, clusterRegistryRuntime ClusterRegistryRuntime, projectRuntime ProjectRuntime, clusterSnapshotRuntime ClusterSnapshotRuntime, clusterDecommissionRuntime ClusterDecommissionRuntime, controlPlaneSnapshotRuntime ControlPlaneSnapshotRuntime, deferredRuntime DeferredRuntime, kubectlRuntime KubectlSessionReapRuntime, securityRuntime SecurityIngestRuntime, crdOwnershipRuntime CRDOwnershipRuntime) error {
	coreRuntime = coreRuntime.normalized()
	required := []requiredRuntimeDependency{
		{name: "runtime.queries", value: coreRuntime.Deps.Queries},
		{name: "runtime.leader", value: coreRuntime.Deps.Leader},
		{name: "runtime.k8s_requester", value: coreRuntime.Deps.K8s},
		{name: "runtime.resource_decryptor", value: coreRuntime.Deps.ResourceDecryptor},
		{name: "security_ingest.queries", value: securityRuntime.Deps.Queries},
		{name: "security_ingest.k8s_fetcher", value: securityRuntime.Deps.K8s},
		{name: "security_ingest.task_outbox", value: securityRuntime.Deps.Outbox},
		{name: "security_ingest.leader", value: securityRuntime.Leader},
		{name: "project_reconcile.queries", value: projectRuntime.Deps.Queries},
		{name: "project_reconcile.requester", value: projectRuntime.Deps.Requester},
		{name: "project_reconcile.encryptor", value: projectRuntime.Deps.Encryptor},
		{name: "cluster_template.queries", value: clusterTemplateRuntime.Deps.Queries},
		{name: "cluster_template.installer", value: clusterTemplateRuntime.Deps.Installer},
		{name: "cluster_template.recovery_enqueuer", value: clusterTemplateRuntime.RecoveryEnqueuer},
		{name: "cluster_registry.queries", value: clusterRegistryRuntime.Deps.Queries},
		{name: "cluster_registry.requester", value: clusterRegistryRuntime.Deps.Requester},
		{name: "cluster_registry.encryptor", value: clusterRegistryRuntime.Deps.Encryptor},
		{name: "cluster_snapshot.queries", value: clusterSnapshotRuntime.Deps.Queries},
		{name: "cluster_snapshot.driver", value: clusterSnapshotRuntime.Deps.Driver},
		{name: "cloud_credentials.queries", value: cloudCredentialRuntime.Deps.Queries},
		{name: "cloud_credentials.requester", value: cloudCredentialRuntime.Deps.Requester},
		{name: "cloud_credentials.decryptor", value: cloudCredentialRuntime.Deps.Decryptor},
		{name: "network_policy.queries", value: networkPolicyRuntime.Deps.Queries},
		{name: "network_policy.requester", value: networkPolicyRuntime.Deps.Requester},
		{name: "mesh_detect.queries", value: meshRuntime.Deps.Queries},
		{name: "mesh_detect.requester", value: meshRuntime.Deps.Requester},
		{name: "cluster_group.metrics_refresher", value: ClusterGroupMetricsRefresher},
		{name: "tool_drift.queries", value: toolDriftRuntime.Deps.Queries},
		{name: "tool_drift.helm", value: toolDriftRuntime.Deps.Helm},
		{name: "cluster_decommission.queries", value: clusterDecommissionRuntime.Deps.Queries},
		{name: "cluster_decommission.tunnel", value: clusterDecommissionRuntime.Deps.Tunnel},
		{name: "cluster_decommission.rbac_cache", value: clusterDecommissionRuntime.Deps.RBACCache},
		{name: "deferred_dispatch.queries", value: deferredRuntime.Deps.Queries},
	}

	for _, operationType := range requiredDeferredReplayTypes {
		required = append(required, requiredRuntimeDependency{
			name:  "deferred_dispatch.replayer[" + operationType + "]",
			value: deferredRuntime.Deps.Replayers[operationType],
		})
	}
	if features.KubectlShell {
		required = append(required,
			requiredRuntimeDependency{name: "kubectl_reaper.queries", value: kubectlRuntime.Deps.Queries},
			requiredRuntimeDependency{name: "kubectl_reaper.requester", value: kubectlRuntime.Deps.Requester},
			requiredRuntimeDependency{name: "kubectl_reaper.leader", value: kubectlRuntime.Leader},
		)
	}
	if features.ControlPlaneSnapshots {
		required = append(required,
			requiredRuntimeDependency{name: "control_plane_snapshot.queries", value: controlPlaneSnapshotRuntime.Deps.Queries},
			requiredRuntimeDependency{name: "control_plane_snapshot.applier", value: controlPlaneSnapshotRuntime.Applier},
			requiredRuntimeDependency{name: "control_plane_snapshot.status_reader", value: controlPlaneSnapshotRuntime.StatusReader},
		)
	}
	if features.CharlieProductBridge {
		required = append(required, requiredRuntimeDependency{name: "charlie.trigger_dispatcher", value: charlieTriggerRuntime.Dispatcher()})
	}
	if features.CRDOwnership {
		required = append(required,
			requiredRuntimeDependency{name: "crd_ownership.queries", value: crdOwnershipRuntime.Deps.Queries},
			requiredRuntimeDependency{name: "crd_ownership.dynamic_client", value: crdOwnershipRuntime.Deps.Dynamic},
		)
	}

	return validateRequiredRuntimeDependencies("tunnel worker", required)
}

func validateRequiredRuntimeDependencies(owner string, dependencies []requiredRuntimeDependency) error {
	missing := make([]string, 0)
	for _, dependency := range dependencies {
		if !runtimeDependencyAvailable(dependency.value) {
			missing = append(missing, dependency.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%s runtime is incomplete; missing: %s", owner, strings.Join(missing, ", "))
}

func runtimeDependencyAvailable(value any) bool {
	if value == nil {
		return false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !reflected.IsNil()
	default:
		return true
	}
}
