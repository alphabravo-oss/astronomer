package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func deploymentWithUpgradeStatus(record upgradeStatusRecord) *fake.Clientset {
	deploy := agentDeploymentFixture()
	body, _ := json.Marshal(record)
	deploy.Annotations = map[string]string{agentUpgradeStatusAnnotation: string(body)}
	return fake.NewClientset(deploy)
}

func collectSent(sent *[]*protocol.Message) func(*protocol.Message) error {
	return func(msg *protocol.Message) error {
		*sent = append(*sent, msg)
		return nil
	}
}

// This is the delivery half of "the new agent never connects": the watchdog
// rolled back without the control plane, and the OLD agent — the one that came
// back — carries the verdict up the tunnel whenever the tunnel returns.
//
// Pre-fix behaviour: no rollback existed, so no failure was ever reported; the
// operation was already marked `succeeded` by the patch ack.
func TestReportPendingUpgradeOutcomeReportsRollbackFailureOnce(t *testing.T) {
	client := deploymentWithUpgradeStatus(upgradeStatusRecord{
		OperationID:   "op-1",
		Phase:         upgradePhaseRolledBack,
		Error:         "ImagePullBackOff: Back-off pulling image",
		TargetImage:   testTargetImage,
		RollbackImage: testCurrentImage,
		RecordedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	var sent []*protocol.Message

	if err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName, collectSent(&sent)); err != nil {
		t.Fatalf("ReportPendingUpgradeOutcome: %v", err)
	}
	if len(sent) != 1 || sent[0].Type != protocol.MsgAgentUpgradeResult {
		t.Fatalf("sent = %+v", sent)
	}
	var result protocol.AgentUpgradeResultPayload
	if err := json.Unmarshal(sent[0].Payload, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRolledBack {
		t.Fatalf("result = %+v, want a terminal failure", result)
	}
	if result.OperationID != "op-1" || result.ClusterID != "cluster-1" {
		t.Fatalf("result identity = %+v", result)
	}
	if !strings.Contains(result.Error, "ImagePullBackOff") {
		t.Fatalf("result error = %q", result.Error)
	}

	// Reporting is once: the record is cleared after a successful send, so a
	// reconnect loop cannot re-fail an operation that has been superseded.
	sent = nil
	if err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName, collectSent(&sent)); err != nil {
		t.Fatalf("second ReportPendingUpgradeOutcome: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("second call sent %d messages, want 0", len(sent))
	}
}

// A send failure must leave the record in place: the tunnel is exactly what is
// unreliable here, and losing the verdict would leave the operation to the
// generic sweeper with no reason attached.
func TestReportPendingUpgradeOutcomeKeepsTheRecordWhenTheSendFails(t *testing.T) {
	client := deploymentWithUpgradeStatus(upgradeStatusRecord{
		OperationID: "op-1",
		Phase:       upgradePhaseRolledBack,
		Error:       "boom",
	})
	err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName,
		func(*protocol.Message) error { return fmt.Errorf("tunnel closed") })
	if err == nil {
		t.Fatal("ReportPendingUpgradeOutcome returned nil, want the send error")
	}
	deploy, getErr := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("get deployment: %v", getErr)
	}
	if deploy.Annotations[agentUpgradeStatusAnnotation] == "" {
		t.Fatal("verdict was cleared even though the send failed")
	}
}

// The PRIMARY success edge is still the replacement agent's heartbeat, observed
// server-side. But since the patch ack no longer completes the operation, that
// edge is a single exact match between two independently-configured strings (the
// chart's image.agent.tag and the binary's version ldflag); if they disagree, a
// perfectly successful upgrade sits in `running`, is re-dispatched every 5
// minutes, and is finally reported FAILED by the sweeper. So the watchdog's own
// verdict is relayed too, keyed on OPERATION ID, as a redundant edge.
//
// This deliberately replaces the previous assertion that the agent never reports
// success. That rule was written when the ack completed the operation and the
// heartbeat path was dead weight; with the ack demoted it left success resting
// on one fragile comparison.
func TestReportPendingUpgradeOutcomeRelaysTheWatchdogSuccessVerdict(t *testing.T) {
	client := deploymentWithUpgradeStatus(upgradeStatusRecord{
		OperationID: "op-1",
		Phase:       upgradePhaseSucceeded,
		TargetImage: testTargetImage,
	})
	var sent []*protocol.Message
	if err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName, collectSent(&sent)); err != nil {
		t.Fatalf("ReportPendingUpgradeOutcome: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d messages for a succeeded rollout, want 1", len(sent))
	}
	var result protocol.AgentUpgradeResultPayload
	if err := json.Unmarshal(sent[0].Payload, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !result.Success || result.Phase != protocol.AgentUpgradePhaseSucceeded || result.OperationID != "op-1" {
		t.Fatalf("result = %+v", result)
	}
	if result.ObservedImage != testTargetImage {
		t.Fatalf("observed image = %q, want the target image", result.ObservedImage)
	}

	// Reported once: the annotation is cleared so a reconnect does not re-send.
	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if _, ok := deploy.Annotations[agentUpgradeStatusAnnotation]; ok {
		t.Fatal("the status annotation survived a delivered success report")
	}
}

// superseded means a different image landed first — there is no outcome for THIS
// operation to report, so the record is cleared silently.
func TestReportPendingUpgradeOutcomeStaysSilentOnSuperseded(t *testing.T) {
	client := deploymentWithUpgradeStatus(upgradeStatusRecord{
		OperationID: "op-1",
		Phase:       upgradePhaseSuperseded,
		TargetImage: testTargetImage,
	})
	var sent []*protocol.Message
	if err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName, collectSent(&sent)); err != nil {
		t.Fatalf("ReportPendingUpgradeOutcome: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent %d messages for a superseded rollout, want 0", len(sent))
	}
}

func TestReportPendingUpgradeOutcomeIsANoOpWithoutAVerdict(t *testing.T) {
	client := fake.NewClientset(agentDeploymentFixture())
	var sent []*protocol.Message
	if err := ReportPendingUpgradeOutcome(context.Background(), client, slog.New(slog.DiscardHandler),
		"cluster-1", DefaultAgentNamespace, DefaultAgentDeploymentName, collectSent(&sent)); err != nil {
		t.Fatalf("ReportPendingUpgradeOutcome: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %+v", sent)
	}
}
