package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func (h *MonitoringHandler) processPendingMonitoringOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside it.
	// Same-target double-dispatch is still prevented by the supersession
	// pass below + DB row "running" state.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingMonitoringOperations(ctx))
}

// claimPendingMonitoringOperations supersedes stale targets and marks
// this tick's claims "running" while holding h.mu. Returned rows are
// wrapped as claimedOps; dispatchClaimed runs them outside the lock.
// Monitoring is special in that the OnFailure closure also re-emits the
// retry/requeue policy when AttemptCount < maxAttempts — that's why we
// capture maxAttempts at claim time (so we don't have to re-parse the
// payload from the dispatch goroutine).
func (h *MonitoringHandler) claimPendingMonitoringOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingMonitoringOperations(ctx, 20)
	if err != nil {
		if h.log != nil {
			h.log.Warn("failed to list pending monitoring operations", "error", err)
		}
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.MonitoringOperation]{
		ID:        func(op sqlc.MonitoringOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.MonitoringOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.MonitoringOperation) string { return op.Status },
		ShouldSupersede: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.Status == OpStatusPending || op.Status == OpStatusRunning && (!op.StartedAt.Valid || now.Sub(op.StartedAt.Time) >= 2*time.Minute)
		},
		IsFreshRunning: func(op sqlc.MonitoringOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < 2*time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.MonitoringOperation) {
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkMonitoringOperationSuperseded(ctx, sqlc.MarkMonitoringOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.MonitoringOperation) (sqlc.MonitoringOperation, error) {
			running, err := h.queries.MarkMonitoringOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.MonitoringOperation{}, err
			}
			maxAttempts := h.operationMaxAttempts(running.Payload)
			h.recordMonitoringOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
				"maxAttempts":   maxAttempts,
			})
			return running, nil
		},
		Claimed: func(running sqlc.MonitoringOperation) claimedOp {
			maxAttempts := h.operationMaxAttempts(running.Payload)
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeMonitoringOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordMonitoringOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkMonitoringOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordMonitoringOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{
						"error": err.Error(),
					})
					_, _ = h.queries.MarkMonitoringOperationFailed(ctx, sqlc.MarkMonitoringOperationFailedParams{ID: running.ID, ErrorMessage: err.Error()})
					if running.AttemptCount < maxAttempts {
						h.recordMonitoringOperationEvent(ctx, running.ID, "warn", "retry", "operation requeued by retry policy", map[string]any{
							"attemptCount": running.AttemptCount,
							"maxAttempts":  maxAttempts,
						})
						_, _ = h.queries.RequeueMonitoringOperation(ctx, running.ID)
					}
					if h.log != nil {
						h.log.Warn("monitoring operation failed", "id", running.ID.String(), "target_type", running.TargetType, "operation_type", running.OperationType, "error", err)
					}
				},
			}
		},
	})
}

func (h *MonitoringHandler) executeMonitoringOperation(ctx context.Context, op sqlc.MonitoringOperation) error {
	var env monitoringOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	switch op.TargetType {
	case "shared_thanos":
		var req SharedThanosStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Thanos upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedThanosStack(ctx, protocol.MsgHelmUpgrade, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedThanosReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedThanosStack(ctx, protocol.MsgHelmInstall, req, valueOrZeroSecret(env.SecretSpec), env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedThanosReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Thanos release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "shared_alertmanager":
		var req SharedAlertmanagerRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Alertmanager upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedAlertmanager(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedAlertmanagerReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedAlertmanager(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			return h.verifySharedAlertmanagerReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Alertmanager release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "shared_grafana":
		var req SharedGrafanaRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Grafana install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedGrafanaStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			if err := h.verifySharedGrafanaReadiness(ctx, op.ID, req); err != nil {
				return err
			}
			h.TriggerGrafanaFolderReconcile()
			return nil
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Grafana upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedGrafanaStack(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedGrafanaReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			h.TriggerGrafanaFolderReconcile()
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedGrafanaStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 90*time.Second); err != nil {
				return err
			}
			if err := h.verifySharedGrafanaReadiness(ctx, op.ID, req); err != nil {
				return err
			}
			h.TriggerGrafanaFolderReconcile()
			return nil
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Grafana release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			// Out-of-band Thanos datasource CM is Helm-adopt annotated, but
			// it is not in the release history until a later Grafana upgrade
			// imports it. Delete it here so a reinstall does not hit
			// "resource already exists and cannot be imported".
			h.deleteGrafanaThanosDatasourceConfigMap(ctx, req)
			h.TriggerGrafanaFolderReconcile()
			return nil
		}
	case "shared_loki":
		var req SharedLokiRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Loki install", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedLokiStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedLokiReadiness(ctx, op.ID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, req.ManagementClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying shared Loki upgrade", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applySharedLokiStack(ctx, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifySharedLokiReadiness(ctx, op.ID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, req.ManagementClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing shared Loki release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement shared Loki release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applySharedLokiStack(ctx, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, req.ManagementClusterID, req.Namespace, req.ReleaseName, 1, 2*time.Minute); err != nil {
				return err
			}
			return h.verifySharedLokiReadiness(ctx, op.ID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling shared Loki release", map[string]any{"clusterId": req.ManagementClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, req.ManagementClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	case "cluster_stack":
		var req MonitoringStackRequest
		if err := json.Unmarshal(env.Request, &req); err != nil {
			return err
		}
		switch op.OperationType {
		case "install":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring install", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "upgrade":
			previousRevision := h.currentReleaseRevision(ctx, env.ClusterID, req.ReleaseName, req.Namespace)
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "render", "applying cluster monitoring upgrade", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmUpgrade, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			if err := h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req); err != nil {
				return h.rollbackIfConfigured(ctx, op.ID, err, env.ResolvedAutoRollback, env.ClusterID, req.ReleaseName, req.Namespace, previousRevision)
			}
			return nil
		case "replace":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling existing cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 900})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "install", "installing replacement cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err = h.applyMonitoringStack(ctx, env.ClusterID, protocol.MsgHelmInstall, req, env.Values)
			if err != nil {
				return err
			}
			if err := h.waitForReleaseReadiness(ctx, op.ID, env.ClusterID, req.Namespace, req.ReleaseName, 2, 2*time.Minute); err != nil {
				return err
			}
			return h.verifyClusterMonitoringReadiness(ctx, op.ID, env.ClusterID, req)
		case "uninstall":
			h.recordMonitoringOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling cluster monitoring release", map[string]any{"clusterId": env.ClusterID, "releaseName": req.ReleaseName, "namespace": req.Namespace})
			_, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: req.ReleaseName, Namespace: req.Namespace, Timeout: 600})
			if err != nil && !isReleaseMissing(err) {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported monitoring operation: %s/%s", op.TargetType, op.OperationType)
}

func isReleaseMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "release: not found")
}

func valueOrZeroSecret(spec *objectStoreSecretSpec) objectStoreSecretSpec {
	if spec == nil {
		return objectStoreSecretSpec{}
	}
	return *spec
}
