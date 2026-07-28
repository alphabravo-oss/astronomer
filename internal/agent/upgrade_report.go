package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ReportPendingUpgradeOutcome closes the loop on a self-upgrade that failed.
//
// The agent that issued an upgrade is killed by its own rollout (strategy:
// Recreate), so it can never report the outcome. The in-cluster watchdog rolls
// the Deployment back and records WHY on the Deployment's annotations; the
// agent that comes back up — the old, working one — reads that record on its
// first connect and delivers it to the control plane. That is why the failure
// path needs no control-plane reachability at the moment of failure: the
// verdict is durable in the cluster and is delivered whenever the tunnel
// returns, minutes or hours later.
//
// The PRIMARY success path does not go through here. Success is the replacement
// agent reconnecting and heartbeating the target version, which the server
// observes directly (MarkRunningAgentUpgradeSucceededByVersion) — a stronger
// signal than anything the agent could self-assert. A watchdog-recorded
// `succeeded` verdict is nevertheless relayed as a redundant, operation-ID-keyed
// edge (reportSucceededUpgrade), so success does not hinge solely on two
// independently-configured version strings agreeing.
//
// Reporting is at-least-once: the record is cleared only after the send
// succeeds, and a duplicate report is idempotent server-side.
func ReportPendingUpgradeOutcome(ctx context.Context, client kubernetes.Interface, log *slog.Logger, clusterID, namespace, deployment string, send func(*protocol.Message) error) error {
	if client == nil || send == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	if strings.TrimSpace(namespace) == "" {
		namespace = DefaultAgentNamespace
	}
	if strings.TrimSpace(deployment) == "" {
		deployment = DefaultAgentDeploymentName
	}

	deploy, err := client.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("read agent deployment for upgrade report: %w", err)
	}
	raw := strings.TrimSpace(deploy.Annotations[agentUpgradeStatusAnnotation])
	if raw == "" {
		return nil
	}
	var record upgradeStatusRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		log.Warn("agent upgrade status annotation is not decodable; clearing it", "error", err)
		clearUpgradeStatusAnnotation(ctx, client, namespace, deployment)
		return nil
	}
	if record.Reported || strings.TrimSpace(record.OperationID) == "" {
		return nil
	}
	switch record.Phase {
	case upgradePhaseRolledBack, upgradePhaseStuck:
		// Failure report, built below.
	case upgradePhaseSucceeded:
		return reportSucceededUpgrade(ctx, client, log, clusterID, namespace, deployment, record, send)
	default:
		// superseded (a different image landed first) and anything unknown:
		// nothing the control plane can act on.
		clearUpgradeStatusAnnotation(ctx, client, namespace, deployment)
		return nil
	}

	detail := strings.TrimSpace(record.Error)
	if detail == "" {
		detail = "agent upgrade did not become healthy"
	}
	if record.Phase == upgradePhaseStuck {
		detail = "agent upgrade failed and could not be rolled back automatically: " + detail
	}
	body, err := json.Marshal(protocol.AgentUpgradeResultPayload{
		OperationID:   record.OperationID,
		ClusterID:     clusterID,
		Success:       false,
		Phase:         protocol.AgentUpgradePhaseRolledBack,
		Error:         detail,
		ObservedImage: record.RollbackImage,
		RollbackImage: record.RollbackImage,
	})
	if err != nil {
		return fmt.Errorf("encode agent upgrade rollback report: %w", err)
	}
	if err := send(&protocol.Message{
		Type:      protocol.MsgAgentUpgradeResult,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		// Leave the annotation in place: the next connect retries it.
		return fmt.Errorf("send agent upgrade rollback report: %w", err)
	}
	log.Warn("reported failed agent self-upgrade to the control plane",
		"operation_id", record.OperationID,
		"phase", record.Phase,
		"target_image", record.TargetImage,
		"rollback_image", record.RollbackImage,
	)
	clearUpgradeStatusAnnotation(ctx, client, namespace, deployment)
	return nil
}

// reportSucceededUpgrade relays the watchdog's success verdict keyed by
// OPERATION ID.
//
// This is a backstop, not the primary signal. Success is normally observed
// server-side when the replacement agent heartbeats the target version
// (MarkRunningAgentUpgradeSucceededByVersion). But that comparison is between
// two independently-configured strings — the chart's image.agent.tag and the
// binary's version ldflag — and since the patch ack no longer completes the
// operation, a mismatch there would turn every successful upgrade into a
// 30-minute wait followed by a sweeper-reported FAILURE, which for a batched
// rollout reads as a fleet-wide failure. The annotation survives on the
// Deployment until it is reported, so this edge fires on whatever connect comes
// after the watchdog wrote it. Duplicate success reports are idempotent
// server-side.
func reportSucceededUpgrade(ctx context.Context, client kubernetes.Interface, log *slog.Logger, clusterID, namespace, deployment string, record upgradeStatusRecord, send func(*protocol.Message) error) error {
	body, err := json.Marshal(protocol.AgentUpgradeResultPayload{
		OperationID:   record.OperationID,
		ClusterID:     clusterID,
		Success:       true,
		Phase:         protocol.AgentUpgradePhaseSucceeded,
		Message:       "replacement agent became available on the target image",
		ObservedImage: record.TargetImage,
		RollbackImage: record.RollbackImage,
	})
	if err != nil {
		return fmt.Errorf("encode agent upgrade success report: %w", err)
	}
	if err := send(&protocol.Message{
		Type:      protocol.MsgAgentUpgradeResult,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}); err != nil {
		// Leave the annotation in place: the next connect retries it.
		return fmt.Errorf("send agent upgrade success report: %w", err)
	}
	log.Info("reported successful agent self-upgrade to the control plane",
		"operation_id", record.OperationID,
		"target_image", record.TargetImage,
	)
	clearUpgradeStatusAnnotation(ctx, client, namespace, deployment)
	return nil
}

// terminalUpgradeVerdict reports whether the watchdog has already declared this
// operation a failure on this Deployment. It is the agent's guard against a
// redelivered upgrade command re-running an upgrade that is known to be bad.
func terminalUpgradeVerdict(deploy *appsv1.Deployment, operationID string) (string, bool) {
	if deploy == nil || strings.TrimSpace(operationID) == "" {
		return "", false
	}
	raw := strings.TrimSpace(deploy.Annotations[agentUpgradeStatusAnnotation])
	if raw == "" {
		return "", false
	}
	var record upgradeStatusRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return "", false
	}
	if record.OperationID != operationID {
		return "", false
	}
	if record.Phase != upgradePhaseRolledBack && record.Phase != upgradePhaseStuck {
		return "", false
	}
	detail := strings.TrimSpace(record.Error)
	if detail == "" {
		detail = record.Phase
	}
	return detail, true
}

func clearUpgradeStatusAnnotation(ctx context.Context, client kubernetes.Interface, namespace, deployment string) {
	_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := client.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if _, ok := deploy.Annotations[agentUpgradeStatusAnnotation]; !ok {
			return nil
		}
		next := deploy.DeepCopy()
		delete(next.Annotations, agentUpgradeStatusAnnotation)
		_, err = client.AppsV1().Deployments(namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}
