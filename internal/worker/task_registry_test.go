package worker

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

func TestTaskRegistryHasOneOwnerAndCompleteExecutionMetadata(t *testing.T) {
	if err := ValidateTaskRegistry(); err != nil {
		t.Fatal(err)
	}
	for _, item := range TaskDescriptors() {
		if !item.RuntimeBound {
			t.Errorf("task %s is not bound through an explicit runtime", item.Type)
		}
		if item.RetryClass == "" || item.IdempotencyClass == "" || item.TerminalState == "" || item.Recovery == "" {
			t.Errorf("task %s has incomplete execution metadata: %+v", item.Type, item)
		}
		if item.Owner == TaskOwnerTunnel && item.Queue != TunnelQueueName {
			t.Errorf("tunnel-owned task %s is routed to %q", item.Type, item.Queue)
		}
		if item.Owner == TaskOwnerWorker && item.Queue == TunnelQueueName {
			t.Errorf("standalone-worker task %s is routed to tunnel", item.Type)
		}
		if item.OptionalFeature != "" && len(item.RequiredCapabilities) == 0 {
			t.Errorf("optional task %s declares no activation capability", item.Type)
		}
	}
}

func TestExplicitRuntimeDescriptorsCannotFallBackToGlobals(t *testing.T) {
	runtimeTypes := map[string]bool{
		tasks.DeliverySourceResolutionType:           false,
		tasks.DeliveryRolloutReconcileType:           false,
		tasks.DeliverySystemRolloutReconcileType:     false,
		tasks.EmailDispatchType:                      false,
		tasks.EmailCleanupOldType:                    false,
		tasks.WebhookDispatchType:                    false,
		tasks.WebhookCleanupOldType:                  false,
		tasks.SIEMDispatchType:                       false,
		tasks.SIEMCleanupOldType:                     false,
		tasks.TaskOutboxDispatchType:                 false,
		tasks.AuditOutboxDispatchType:                false,
		tasks.CharlieAlertDispatchTaskType:           false,
		tasks.CharlieAlertReconcileType:              false,
		tasks.AgentTokenRotateSweepType:              false,
		tasks.PlaintextCredentialMigrationType:       false,
		tasks.CRDOwnershipDriftCheckType:             false,
		tasks.ApiserverAllowlistReconcileType:        false,
		tasks.ApiserverAllowlistReconcileAllType:     false,
		tasks.ApiserverAllowlistCleanupSnapshotsType: false,
		tasks.GitOpsSyncType:                         false,
		tasks.ToolDriftSweepType:                     false,
		tasks.CharlieTriggerDispatchType:             false,
		tasks.ClusterTemplateApplyType:               false,
		tasks.ClusterTemplateDriftCheckType:          false,
		tasks.NetworkPolicyApplyType:                 false,
		tasks.NetworkPolicyDriftCheckType:            false,
		tasks.MeshDetectType:                         false,
		tasks.CloudCredentialMaterializeType:         false,
		tasks.CloudCredentialDriftReconcileType:      false,
		tasks.ClusterApplyRegistrySecretType:         false,
		tasks.ClusterRegistryDriftReconcileType:      false,
		tasks.ProjectReconcileType:                   false,
		tasks.ProjectReconcileAllType:                false,
		tasks.ClusterSnapshotPollType:                false,
		tasks.ClusterSnapshotDispatchScheduledType:   false,
		tasks.ClusterSnapshotCleanupExpiredType:      false,
		tasks.ClusterDecommissionType:                false,
		tasks.ClusterDecommissionAllType:             false,
		tasks.ControlPlaneSnapshotSweepType:          false,
		tasks.DispatchDeferredType:                   false,
		tasks.KubectlSessionReapType:                 false,
		tasks.SecurityIngestType:                     false,
		tasks.SecurityIngestRecoveryType:             false,
	}
	for _, item := range TaskDescriptors() {
		if _, ok := runtimeTypes[item.Type]; !ok {
			continue
		}
		if !item.RuntimeBound {
			t.Errorf("task %q can fall back to process-global wiring", item.Type)
		}
		runtimeTypes[item.Type] = true
	}
	for taskType, found := range runtimeTypes {
		if !found {
			t.Errorf("runtime-bound task %q missing from registry", taskType)
		}
	}
}

func TestTaskCapabilityValidationFailsClosed(t *testing.T) {
	err := ValidateTaskCapabilities(TaskOwnerWorker, map[TaskCapability]bool{
		CapabilityDatabase: true,
		CapabilityRedis:    true,
	})
	if err == nil || !strings.Contains(err.Error(), string(CapabilityEncryption)) {
		t.Fatalf("expected deterministic missing-capability error, got %v", err)
	}
}

func TestLegacyBackupTasksRemainConsumableButUnscheduled(t *testing.T) {
	descriptors := map[string]TaskDescriptor{}
	for _, item := range TaskDescriptors() {
		descriptors[item.Type] = item
	}

	for _, taskType := range []string{TypeRunScheduledBackups, TypeEnforceBackupRetention} {
		item, ok := descriptors[taskType]
		if !ok {
			t.Fatalf("legacy backup task %s has no compatibility consumer", taskType)
		}
		if item.Handler == nil {
			t.Errorf("legacy backup task %s has a nil handler", taskType)
		}
		if len(item.Schedules) != 0 {
			t.Errorf("legacy backup task %s must not be scheduled: %+v", taskType, item.Schedules)
		}
	}
}
