package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

// rolledOutDeployment is the state of the world immediately after the agent has
// patched itself: the pod template carries this operation's annotation and the
// target image, and the Deployment status reflects whether the replacement pod
// ever became Available (i.e. whether the new agent reconnected — the agent's
// readinessProbe is /readyz, which is 200 only while the tunnel is up).
func rolledOutDeployment(operationID, image string, available bool) *appsv1.Deployment {
	deploy := agentDeploymentFixture()
	deploy.Generation = 2
	deploy.Spec.Replicas = ptr.To(int32(1))
	deploy.Spec.Template.Spec.Containers[0].Image = image
	deploy.Spec.Template.Annotations = map[string]string{agentUpgradeOperationAnnotation: operationID}
	deploy.Status = appsv1.DeploymentStatus{
		ObservedGeneration: 2,
		Replicas:           1,
		UpdatedReplicas:    1,
	}
	if available {
		deploy.Status.AvailableReplicas = 1
	} else {
		deploy.Status.UnavailableReplicas = 1
	}
	return deploy
}

func watchdogTestOptions(operationID string) UpgradeWatchdogOptions {
	return UpgradeWatchdogOptions{
		Namespace:      DefaultAgentNamespace,
		Deployment:     DefaultAgentDeploymentName,
		OperationID:    operationID,
		TargetImage:    testTargetImage,
		RollbackImage:  testCurrentImage,
		PatchTimeout:   50 * time.Millisecond,
		RolloutTimeout: 50 * time.Millisecond,
		PollInterval:   time.Millisecond,
	}
}

func currentAgentImage(t *testing.T, client *fake.Clientset) string {
	t.Helper()
	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return agentImageOf(deploy)
}

func recordedUpgradeStatus(t *testing.T, client *fake.Clientset) upgradeStatusRecord {
	t.Helper()
	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	raw := deploy.Annotations[agentUpgradeStatusAnnotation]
	if raw == "" {
		t.Fatalf("no %s annotation on the deployment", agentUpgradeStatusAnnotation)
	}
	var record upgradeStatusRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("decode upgrade status: %v", err)
	}
	return record
}

// The failure this whole item exists to survive: the new agent NEVER CONNECTS.
// There is no result message, no heartbeat, and the control plane is
// unreachable from the cluster's point of view — the only actor left is the
// watchdog, which must notice the timeout and restore the previous image.
//
// Pre-fix behaviour: nothing rolled back. RollbackImage was computed in
// internal/handler/agent_fleet.go, rendered as prose, and never used by any
// code path; the Deployment stayed on the broken image until an operator ran
// kubectl against every affected cluster.
func TestUpgradeWatchdogRollsBackWhenReplacementNeverBecomesAvailable(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-1", testTargetImage, false))
	opts := watchdogTestOptions("op-1")

	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), opts); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}

	if got := currentAgentImage(t, client); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it rolled back to %q", got, testCurrentImage)
	}
	record := recordedUpgradeStatus(t, client)
	if record.Phase != upgradePhaseRolledBack || record.OperationID != "op-1" {
		t.Fatalf("status record = %+v", record)
	}
	if record.RollbackImage != testCurrentImage || record.TargetImage != testTargetImage {
		t.Fatalf("status record images = %+v", record)
	}
	if !strings.Contains(record.Error, "did not become ready") {
		t.Fatalf("recorded error = %q", record.Error)
	}
}

// The rollback must also re-trigger the rollout, not just edit the spec: with
// strategy Recreate the pod template has to change or the Deployment
// controller has nothing to act on.
func TestUpgradeWatchdogRollbackForcesANewPodTemplateRevision(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-1", testTargetImage, false))
	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-1")); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got := deploy.Spec.Template.Annotations[agentUpgradeOperationAnnotation]; got != "op-1:rollback" {
		t.Fatalf("template annotation = %q, want a distinct rollback revision", got)
	}
	// The verdict must NOT live on the pod template: it would roll the agent
	// every time the watchdog recorded a status.
	if _, ok := deploy.Spec.Template.Annotations[agentUpgradeStatusAnnotation]; ok {
		t.Fatalf("status annotation must not be on the pod template")
	}
}

// The reason the operator sees has to be the kubelet's, not "it timed out".
func TestUpgradeWatchdogRecordsTheKubeletReasonForTheFailedRollout(t *testing.T) {
	deploy := rolledOutDeployment("op-1", testTargetImage, false)
	client := fake.NewClientset(deploy, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "astronomer-agent-new",
			Namespace: DefaultAgentNamespace,
			Labels:    deploy.Spec.Selector.MatchLabels,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "agent",
				Image: testTargetImage,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "ImagePullBackOff",
					Message: "Back-off pulling image " + testTargetImage,
				}},
			}},
		},
	})

	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-1")); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	record := recordedUpgradeStatus(t, client)
	if !strings.Contains(record.Error, "ImagePullBackOff") {
		t.Fatalf("recorded error = %q, want the kubelet reason", record.Error)
	}
}

// Success is "an available replica on the target image", which for this agent
// means the replacement process came up AND reconnected its tunnel.
func TestUpgradeWatchdogLeavesAHealthyRolloutAlone(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-1", testTargetImage, true))
	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-1")); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	if got := currentAgentImage(t, client); got != testTargetImage {
		t.Fatalf("deployment image = %q, want the target image kept", got)
	}
	if record := recordedUpgradeStatus(t, client); record.Phase != upgradePhaseSucceeded {
		t.Fatalf("status record = %+v", record)
	}
}

// The watchdog is created BEFORE the Deployment is patched, so it must tolerate
// an upgrade the agent decided not to commit (rejected image, failed preflight,
// agent crash). Rolling back in that case would bounce a perfectly good agent.
func TestUpgradeWatchdogDoesNothingWhenTheRolloutNeverStarted(t *testing.T) {
	client := fake.NewClientset(agentDeploymentFixture())
	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-1")); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	if got := currentAgentImage(t, client); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it untouched", got)
	}
	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if _, ok := deploy.Annotations[agentUpgradeStatusAnnotation]; ok {
		t.Fatalf("watchdog recorded a verdict for a rollout that never happened")
	}
}

// An operator (or a second operation) correcting the image mid-window must win.
func TestUpgradeWatchdogDoesNotFightAnImageChangedUnderneathIt(t *testing.T) {
	deploy := rolledOutDeployment("op-1", "example.com/astronomer-agent:v2.0.0", false)
	client := fake.NewClientset(deploy)
	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-1")); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	if got := currentAgentImage(t, client); got != "example.com/astronomer-agent:v2.0.0" {
		t.Fatalf("deployment image = %q, want the operator's image preserved", got)
	}
	if record := recordedUpgradeStatus(t, client); record.Phase != upgradePhaseSuperseded {
		t.Fatalf("status record = %+v", record)
	}
}

// Rolling back to the same broken image is not a rollback. Record it as stuck
// so the sweeper and the operator both learn manual recovery is required,
// rather than pretending the situation was handled.
func TestUpgradeWatchdogRecordsStuckWhenThereIsNoDistinctRollbackImage(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-1", testTargetImage, false))
	opts := watchdogTestOptions("op-1")
	opts.RollbackImage = testTargetImage

	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), opts); err != nil {
		t.Fatalf("RunUpgradeWatchdog: %v", err)
	}
	if got := currentAgentImage(t, client); got != testTargetImage {
		t.Fatalf("deployment image = %q", got)
	}
	if record := recordedUpgradeStatus(t, client); record.Phase != upgradePhaseStuck {
		t.Fatalf("status record = %+v", record)
	}
}

// The watchdog is restarted by the Job on failure, so a second run over an
// already rolled-back Deployment must be a no-op rather than a second rollback
// (or a rollback of the rollback).
func TestUpgradeWatchdogIsIdempotentAcrossRestarts(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-1", testTargetImage, false))
	opts := watchdogTestOptions("op-1")
	for i := 0; i < 2; i++ {
		if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), opts); err != nil {
			t.Fatalf("RunUpgradeWatchdog run %d: %v", i, err)
		}
	}
	if got := currentAgentImage(t, client); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want the rollback image", got)
	}
}

func TestBuildWatchdogJobRunsTheKnownGoodImageWithBoundedLifetime(t *testing.T) {
	opts := watchdogTestOptions("op-1")
	opts.applyDefaults()
	job := buildWatchdogJob(opts, agentDeploymentFixture().Spec.Template.Spec, "astronomer-agent")

	container := job.Spec.Template.Spec.Containers[0]
	if container.Image != testCurrentImage {
		t.Fatalf("watchdog image = %q, want the rollback image", container.Image)
	}
	if container.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("watchdog pull policy = %q; it must start from the node's copy even with the registry down", container.ImagePullPolicy)
	}
	if got := strings.Join(container.Command, " "); got != "astronomer-agent "+upgradeWatchdogCommand {
		t.Fatalf("watchdog command = %q", got)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds <= 0 {
		t.Fatalf("watchdog job has no active deadline")
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Fatalf("watchdog job has no TTL and would litter the namespace")
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Fatalf("watchdog restart policy = %q, want OnFailure so a crashed watchdog comes back", job.Spec.Template.Spec.RestartPolicy)
	}
	// These labels are what the shipped astronomer-agent NetworkPolicy selects;
	// without them the watchdog loses egress to the kube-apiserver on clusters
	// running an older manifest and could not roll anything back.
	labels := job.Spec.Template.Labels
	if labels["app.kubernetes.io/name"] != "astronomer-agent" || labels["app.kubernetes.io/component"] != "agent" {
		t.Fatalf("watchdog pod labels = %v", labels)
	}
	env := map[string]string{}
	for _, e := range container.Env {
		env[e.Name] = e.Value
	}
	if env[envWatchdogTargetImage] != testTargetImage || env[envWatchdogRollbackImage] != testCurrentImage {
		t.Fatalf("watchdog env = %v", env)
	}
	if env[envWatchdogOperationID] != "op-1" {
		t.Fatalf("watchdog operation id = %q", env[envWatchdogOperationID])
	}
}
