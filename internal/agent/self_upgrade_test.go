package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	testCurrentImage = "example.com/astronomer-agent:v1.0.0"
	testTargetImage  = "example.com/astronomer-agent:v1.2.3"
)

func agentDeploymentFixture() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultAgentDeploymentName,
			Namespace: DefaultAgentNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name":      "astronomer-agent",
				"app.kubernetes.io/component": "agent",
			}},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					ServiceAccountName: "astronomer-agent",
					Containers: []corev1.Container{
						{Name: "agent", Image: testCurrentImage},
					},
				},
			},
		},
	}
}

// actionRecorder captures the ORDER of writes against the fake API. Ordering is
// load-bearing for self-upgrade: the watchdog Job must exist before the
// Deployment is patched, otherwise a bad rollout has nothing to undo it.
type actionRecorder struct {
	mu      sync.Mutex
	entries []string
}

func (r *actionRecorder) record(verb, resource string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, verb+" "+resource)
}

func (r *actionRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.entries))
	copy(out, r.entries)
	return out
}

func (r *actionRecorder) indexOf(entry string) int {
	for i, got := range r.snapshot() {
		if got == entry {
			return i
		}
	}
	return -1
}

// upgradeFixture wires a fake cluster in which the preflight pull either
// succeeds or fails, and in which the watchdog Job's pod either starts or does
// not. Everything else (Deployment reads, patches) is the real code path.
type upgradeFixture struct {
	client   *fake.Clientset
	handler  *SelfUpgradeHandler
	recorder *actionRecorder
}

type upgradeFixtureOptions struct {
	pullSucceeds   bool
	pullFailReason string
	// pullFailImage makes exactly one image unpullable while pullSucceeds
	// governs the rest — used to fail the ROLLBACK image while the target pulls.
	pullFailImage  string
	watchdogStarts bool
	operationID    string
}

func newUpgradeFixture(t *testing.T, opts upgradeFixtureOptions) *upgradeFixture {
	t.Helper()
	client := fake.NewClientset(agentDeploymentFixture())
	rec := &actionRecorder{}

	client.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		switch action.GetVerb() {
		case "create", "update", "patch", "delete":
			rec.record(action.GetVerb(), action.GetResource().Resource)
		}
		return false, nil, nil
	})

	// Stand in for the kubelet: stamp the preflight pod with the container
	// status a real node would report once it had (or had not) obtained the
	// image. Returning handled=false lets the default tracker store the
	// mutated object, so the subsequent Get sees it.
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		pod, ok := create.GetObject().(*corev1.Pod)
		if !ok || pod.Labels["app.kubernetes.io/component"] != "agent-upgrade-preflight" {
			return false, nil, nil
		}
		if opts.pullSucceeds && pod.Spec.Containers[0].Image != opts.pullFailImage {
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
				Name:  "preflight",
				Image: pod.Spec.Containers[0].Image,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  "RunContainerError",
					Message: "exec: \"" + preflightSentinelCommand + "\": stat: no such file or directory",
				}},
			}}
		} else {
			reason := opts.pullFailReason
			if reason == "" {
				reason = "ImagePullBackOff"
			}
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
				Name:  "preflight",
				Image: pod.Spec.Containers[0].Image,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
					Reason:  reason,
					Message: "Back-off pulling image",
				}},
			}}
		}
		return false, nil, nil
	})

	if opts.watchdogStarts {
		// The Job controller does not run under the fake clientset, so create
		// the pod the watchdog Job would have produced.
		if _, err := client.CoreV1().Pods(DefaultAgentNamespace).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      watchdogJobName(opts.operationID) + "-abcde",
				Namespace: DefaultAgentNamespace,
				Labels:    map[string]string{"job-name": watchdogJobName(opts.operationID)},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed watchdog pod: %v", err)
		}
	}

	handler := NewSelfUpgradeHandler(client, slog.New(slog.DiscardHandler))
	handler.pollIntervalOverride = time.Millisecond
	handler.preflightTimeoutOverride = 2 * time.Second
	handler.watchdogStartWindowOverride = 200 * time.Millisecond
	return &upgradeFixture{client: client, handler: handler, recorder: rec}
}

func (f *upgradeFixture) upgrade(t *testing.T, payload protocol.AgentUpgradePayload) protocol.AgentUpgradeResultPayload {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	resp, err := f.handler.HandleUpgrade(context.Background(), &protocol.Message{
		Type:    protocol.MsgAgentUpgrade,
		Payload: body,
	})
	if err != nil {
		t.Fatalf("HandleUpgrade returned error: %v", err)
	}
	if resp == nil || resp.Type != protocol.MsgAgentUpgradeResult {
		t.Fatalf("response = %+v", resp)
	}
	var result protocol.AgentUpgradeResultPayload
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return result
}

func (f *upgradeFixture) deployedImage(t *testing.T) string {
	t.Helper()
	deploy, err := f.client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return deploy.Spec.Template.Spec.Containers[0].Image
}

func (f *upgradeFixture) watchdogJobs(t *testing.T) int {
	t.Helper()
	jobs, err := f.client.BatchV1().Jobs(DefaultAgentNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return len(jobs.Items)
}

// assertNoRollout is the invariant every rejection test shares: the running
// agent's Deployment is byte-identical to what it was, and no watchdog exists
// because there is nothing to watch.
func (f *upgradeFixture) assertNoRollout(t *testing.T) {
	t.Helper()
	if got := f.deployedImage(t); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it untouched at %q", got, testCurrentImage)
	}
	if n := f.watchdogJobs(t); n != 0 {
		t.Fatalf("watchdog jobs = %d, want 0 (nothing should have been rolled out)", n)
	}
	if i := f.recorder.indexOf("update deployments"); i >= 0 {
		t.Fatalf("deployment was updated at action %d: %v", i, f.recorder.snapshot())
	}
}

// Pre-fix behaviour: patchAgentDeployment validated only TrimSpace+non-empty,
// so "registry.attacker.test/evil:v1" went straight into the container spec and
// the handler answered Success=true.
func TestUpgradeRejectsImageOutsideAllowedRepository(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID: "op-1",
		ClusterID:   "cluster-1",
		TargetImage: "registry.attacker.test/astronomer-agent:v9.9.9",
	})
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
		t.Fatalf("result = %+v, want a rejection", result)
	}
	if !strings.Contains(result.Error, "not the permitted agent image repository") {
		t.Fatalf("error = %q", result.Error)
	}
	fixture.assertNoRollout(t)
}

// A prefix allow-list would pass this; the check is an exact repository match.
func TestUpgradeRejectsRepositoryThatMerelyPrefixMatches(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID: "op-1",
		TargetImage: "example.com/astronomer-agent-attacker:v1.2.3",
	})
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
		t.Fatalf("result = %+v, want a rejection", result)
	}
	fixture.assertNoRollout(t)
}

// Pre-fix behaviour: any non-empty string was accepted, including one that is
// not a parseable image reference at all.
func TestUpgradeRejectsMalformedImageReference(t *testing.T) {
	for name, image := range map[string]string{
		"unqualified":    "example.com/astronomer-agent",
		"space":          "example.com/astronomer agent:v1",
		"bad digest":     "example.com/astronomer-agent@sha256:nothex",
		"path traversal": "example.com/../astronomer-agent:v1",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
			result := fixture.upgrade(t, protocol.AgentUpgradePayload{
				OperationID: "op-1",
				TargetImage: image,
			})
			if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
				t.Fatalf("result = %+v, want a rejection", result)
			}
			fixture.assertNoRollout(t)
		})
	}
}

// A floating tag cannot be verified and cannot be rolled back to a known state:
// the rollback image would be the same string resolving to different bytes.
func TestUpgradeRejectsFloatingTagUnlessExplicitlyAllowed(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	payload := protocol.AgentUpgradePayload{OperationID: "op-1", TargetImage: "example.com/astronomer-agent:latest"}

	result := fixture.upgrade(t, payload)
	if result.Success || !strings.Contains(result.Error, "floating image tag") {
		t.Fatalf("result = %+v, want a floating-tag rejection", result)
	}
	fixture.assertNoRollout(t)

	fixture.handler.SetUpgradePolicy(UpgradePolicy{AllowMutableTag: true})
	if result := fixture.upgrade(t, payload); !result.Success {
		t.Fatalf("result with AllowMutableTag = %+v, want acceptance", result)
	}
}

// Pre-fix behaviour: the image was never pulled before the Deployment was
// patched, so an unpullable image took the cluster dark under strategy
// Recreate and the fleet UI still said "succeeded".
func TestUpgradeRejectsImageThatCannotBePulled(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{
		pullSucceeds:   false,
		pullFailReason: "ImagePullBackOff",
		watchdogStarts: true,
		operationID:    "op-1",
	})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID: "op-1",
		TargetImage: testTargetImage,
	})
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
		t.Fatalf("result = %+v, want a rejection", result)
	}
	if !strings.Contains(result.Error, "ImagePullBackOff") {
		t.Fatalf("error = %q, want the kubelet's pull reason", result.Error)
	}
	fixture.assertNoRollout(t)
}

// The rollback image is the only way back from a bad rollout. If IT cannot be
// pulled, committing the upgrade would be a one-way door.
func TestUpgradeRejectsWhenRollbackImageIsNotPullable(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{
		pullSucceeds:   true,
		pullFailImage:  "example.com/astronomer-agent:v0.9.0",
		pullFailReason: "ErrImagePull",
		watchdogStarts: true,
		operationID:    "op-1",
	})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID:   "op-1",
		TargetImage:   testTargetImage,
		RollbackImage: "example.com/astronomer-agent:v0.9.0",
	})
	if result.Success || !strings.Contains(result.Error, "rollback image is not usable") {
		t.Fatalf("result = %+v, want a rollback-image rejection", result)
	}
	fixture.assertNoRollout(t)
}

// The agent cannot verify its own rollout (strategy Recreate kills it), so the
// watchdog is the only thing that can undo a bad image. Committing without a
// live watchdog would reintroduce the exact defect being fixed.
func TestUpgradeRefusesToRollOutWhenWatchdogNeverStarts(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: false, operationID: "op-1"})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID: "op-1",
		TargetImage: testTargetImage,
	})
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
		t.Fatalf("result = %+v, want a rejection", result)
	}
	if !strings.Contains(result.Error, "did not start") {
		t.Fatalf("error = %q", result.Error)
	}
	if got := fixture.deployedImage(t); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it untouched", got)
	}
	if i := fixture.recorder.indexOf("update deployments"); i >= 0 {
		t.Fatalf("deployment was patched without a live watchdog: %v", fixture.recorder.snapshot())
	}
}

// Pre-fix behaviour: HandleUpgrade set Success=true the instant the Update
// returned, and internal/tunnel/handler.go turned that into StatusSucceeded —
// success was reported BEFORE the rollout had happened at all.
func TestUpgradeReportsRolloutStartedNotSuccessAndOrdersWatchdogFirst(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	result := fixture.upgrade(t, protocol.AgentUpgradePayload{
		OperationID: "op-1",
		ClusterID:   "cluster-1",
		TargetImage: testTargetImage,
	})
	if result.Phase != protocol.AgentUpgradePhaseRolloutStarted {
		t.Fatalf("phase = %q, want %q", result.Phase, protocol.AgentUpgradePhaseRolloutStarted)
	}
	if result.ObservedImage != testTargetImage || result.RollbackImage != testCurrentImage {
		t.Fatalf("result = %+v", result)
	}
	if got := fixture.deployedImage(t); got != testTargetImage {
		t.Fatalf("deployment image = %q, want %q", got, testTargetImage)
	}

	jobIndex := fixture.recorder.indexOf("create jobs")
	patchIndex := fixture.recorder.indexOf("update deployments")
	if jobIndex < 0 || patchIndex < 0 {
		t.Fatalf("actions = %v", fixture.recorder.snapshot())
	}
	if jobIndex > patchIndex {
		t.Fatalf("watchdog job was created AFTER the deployment patch: %v", fixture.recorder.snapshot())
	}

	jobs, err := fixture.client.BatchV1().Jobs(DefaultAgentNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("watchdog jobs = %d, want 1", len(jobs.Items))
	}
	// The watchdog must run the image we KNOW is pullable — the one executing
	// right now — never the unproven target.
	if got := jobs.Items[0].Spec.Template.Spec.Containers[0].Image; got != testCurrentImage {
		t.Fatalf("watchdog image = %q, want the rollback image %q", got, testCurrentImage)
	}
	deploy, err := fixture.client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got := deploy.Spec.Template.Annotations[agentUpgradeOperationAnnotation]; got != "op-1" {
		t.Fatalf("operation annotation = %q", got)
	}
}

// A redelivered upgrade command (the server re-claims operations stuck in
// `running`) must not race a second watchdog against the first.
func TestUpgradeReusesExistingWatchdogForSameOperation(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	payload := protocol.AgentUpgradePayload{OperationID: "op-1", TargetImage: testTargetImage}
	if result := fixture.upgrade(t, payload); !result.Success {
		t.Fatalf("first upgrade = %+v", result)
	}
	if result := fixture.upgrade(t, payload); !result.Success {
		t.Fatalf("redelivered upgrade = %+v", result)
	}
	if n := fixture.watchdogJobs(t); n != 1 {
		t.Fatalf("watchdog jobs = %d, want 1", n)
	}
}

// The server re-claims operations stuck in `running` every five minutes and
// re-sends the command. Retrying an upgrade the watchdog already rolled back
// would take the cluster dark again for every retry, so a recorded terminal
// verdict for the SAME operation is a hard refusal.
func TestUpgradeRefusesToRetryAnOperationAlreadyRolledBack(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	deploy, err := fixture.client.AppsV1().Deployments(DefaultAgentNamespace).Get(
		context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	verdict, _ := json.Marshal(upgradeStatusRecord{
		OperationID:   "op-1",
		Phase:         upgradePhaseRolledBack,
		Error:         "CrashLoopBackOff: back-off restarting failed container",
		TargetImage:   testTargetImage,
		RollbackImage: testCurrentImage,
	})
	deploy.Annotations = map[string]string{agentUpgradeStatusAnnotation: string(verdict)}
	if _, err := fixture.client.AppsV1().Deployments(DefaultAgentNamespace).Update(
		context.Background(), deploy, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}

	result := fixture.upgrade(t, protocol.AgentUpgradePayload{OperationID: "op-1", TargetImage: testTargetImage})
	if result.Success || result.Phase != protocol.AgentUpgradePhaseRejected {
		t.Fatalf("result = %+v, want a rejection", result)
	}
	if !strings.Contains(result.Error, "CrashLoopBackOff") {
		t.Fatalf("error = %q, want the recorded verdict", result.Error)
	}
	if got := fixture.deployedImage(t); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it untouched", got)
	}
	if n := fixture.watchdogJobs(t); n != 0 {
		t.Fatalf("watchdog jobs = %d, want 0", n)
	}

	// A DIFFERENT operation must still be allowed through — an operator fixing
	// the image and re-queuing has to work.
	if _, err := fixture.client.CoreV1().Pods(DefaultAgentNamespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      watchdogJobName("op-2") + "-abcde",
			Namespace: DefaultAgentNamespace,
			Labels:    map[string]string{"job-name": watchdogJobName("op-2")},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed watchdog pod: %v", err)
	}
	if result := fixture.upgrade(t, protocol.AgentUpgradePayload{OperationID: "op-2", TargetImage: testTargetImage}); !result.Success {
		t.Fatalf("follow-up operation = %+v, want acceptance", result)
	}
}

// A watchdog that has already exited is no protection. Committing a rollout
// behind a finished Job would put us back where we started: an image change
// with nothing able to undo it.
func TestUpgradeReplacesAFinishedWatchdogBeforeRollingOut(t *testing.T) {
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, watchdogStarts: true, operationID: "op-1"})
	spent := buildWatchdogJob(UpgradeWatchdogOptions{
		Namespace: DefaultAgentNamespace, Deployment: DefaultAgentDeploymentName,
		OperationID: "op-1", TargetImage: testTargetImage, RollbackImage: testCurrentImage,
	}, agentDeploymentFixture().Spec.Template.Spec, "astronomer-agent")
	spent.Status.Succeeded = 1
	spent.Status.CompletionTime = &metav1.Time{Time: time.Now()}
	if _, err := fixture.client.BatchV1().Jobs(DefaultAgentNamespace).Create(
		context.Background(), spent, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed finished watchdog job: %v", err)
	}

	result := fixture.upgrade(t, protocol.AgentUpgradePayload{OperationID: "op-1", TargetImage: testTargetImage})
	if !result.Success {
		t.Fatalf("result = %+v, want acceptance behind a fresh watchdog", result)
	}
	jobs, err := fixture.client.BatchV1().Jobs(DefaultAgentNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Status.Succeeded != 0 {
		t.Fatalf("jobs = %+v, want exactly one fresh watchdog", jobs.Items)
	}
	deleteIndex := fixture.recorder.indexOf("delete jobs")
	patchIndex := fixture.recorder.indexOf("update deployments")
	if deleteIndex < 0 || patchIndex < 0 || deleteIndex > patchIndex {
		t.Fatalf("spent watchdog was not replaced before the rollout: %v", fixture.recorder.snapshot())
	}
}
