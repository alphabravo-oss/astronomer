package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// TestUpgradeWatchdogRollsBackDespiteATransientReadError is the "rollback does
// not fire" regression.
//
// waitForRolloutHealth used to return on the FIRST failed Get. RunUpgradeWatchdog
// propagated it, the process exited non-zero, and restartPolicy OnFailure
// restarted it — with a FRESH RolloutTimeout measured against a Job
// ActiveDeadlineSeconds that keeps counting from Job start. A restart late in
// the health wait therefore lands past the deadline, and an exceeded
// ActiveDeadline fails a Job PERMANENTLY (unlike backoffLimit). Net effect of one
// transient apiserver blip: no rollback, no status annotation, and a cluster
// sitting dark on the bad image until the 30-minute server-side sweeper emits a
// generic message.
func TestUpgradeWatchdogRollsBackDespiteATransientReadError(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-transient", testTargetImage, false))

	// One failure, mid-health-wait: the first Get succeeds (so the watchdog sees
	// the patch), the second fails, everything after succeeds.
	var gets atomic.Int32
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		if gets.Add(1) == 2 {
			return true, nil, errors.New("etcdserver: request timed out")
		}
		return false, nil, nil
	})

	if err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-transient")); err != nil {
		t.Fatalf("RunUpgradeWatchdog returned %v; a single transient read must cost a poll interval, not the whole rollback budget", err)
	}
	if gets.Load() < 2 {
		t.Fatalf("the injected failure was never reached (%d Gets); the test asserts nothing", gets.Load())
	}
	if got := currentAgentImage(t, client); got != testCurrentImage {
		t.Fatalf("deployment image = %q, want it rolled back to %q", got, testCurrentImage)
	}
	if record := recordedUpgradeStatus(t, client); record.Phase != upgradePhaseRolledBack {
		t.Fatalf("recorded phase = %q, want %q; without the annotation the agent has nothing to report",
			record.Phase, upgradePhaseRolledBack)
	}
}

// TestUpgradeWatchdogFailsWhenItCanNeverReadTheDeployment is the other half of
// the same change: tolerating transient errors must NOT become tolerating a
// total outage. With no successful read at all there is no evidence to roll back
// on, so the run must fail and let the Job restart it.
func TestUpgradeWatchdogFailsWhenItCanNeverReadTheDeployment(t *testing.T) {
	client := fake.NewClientset(rolledOutDeployment("op-dark", testTargetImage, false))
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})

	err := RunUpgradeWatchdog(context.Background(), client, slog.New(slog.DiscardHandler), watchdogTestOptions("op-dark"))
	if err == nil {
		t.Fatal("RunUpgradeWatchdog succeeded without ever reading the Deployment; the Job would not be restarted")
	}
}

// TestWatchdogJobDeadlineCoversARestart pins the interaction the poll-loop fix
// alone does not cover: the process can still die (OOM, node eviction), and the
// Job's ActiveDeadlineSeconds is measured from Job start while restartPolicy
// OnFailure resets the process's own timers. Sized for a single run, a restart
// late in the health wait is killed by the deadline and the Job fails
// permanently — no rollback.
func TestWatchdogJobDeadlineCoversARestart(t *testing.T) {
	opts := watchdogTestOptions("op-deadline")
	opts.applyDefaults()
	job := buildWatchdogJob(opts, agentDeploymentFixture().Spec.Template.Spec, "astronomer-agent")

	if job.Spec.ActiveDeadlineSeconds == nil {
		t.Fatal("watchdog job has no active deadline")
	}
	oneRun := int64(opts.PatchTimeout/time.Second) + int64(opts.RolloutTimeout/time.Second) + 120
	if got := *job.Spec.ActiveDeadlineSeconds; got < 2*oneRun {
		t.Fatalf("active deadline = %ds, want at least %ds so a restarted watchdog can complete one full run; an exceeded ActiveDeadline fails the Job permanently and nothing rolls the agent back",
			got, 2*oneRun)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit <= 0 {
		t.Fatal("watchdog job has no backoff limit, so the deadline is the only restart budget")
	}
}

// TestConcurrentUpgradeRedeliveryRunsExactlyOnce is the redelivery regression.
//
// The server re-claims and re-dispatches any operation still `running` after 5
// minutes, and nothing refreshes updated_at between dispatch and result while
// the pre-commit path (target preflight + rollback preflight + watchdog start)
// can legitimately take longer than that. MsgAgentUpgrade is dispatchExempt, so
// the redelivery always spawns a second handler goroutine.
//
// Before the guard, both attempts raced over the same deterministically-named
// preflight pod: attempt B's unconditional pre-delete removed attempt A's
// IN-FLIGHT pod, A's next Get returned NotFound, and A reported
// Success=false/rejected — marking the operation FAILED while B went on to
// patch the Deployment. A batched fleet rollout halts on that false failure.
func TestConcurrentUpgradeRedeliveryRunsExactlyOnce(t *testing.T) {
	const operationID = "op-redelivered"
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{
		pullSucceeds:   true,
		watchdogStarts: true,
		operationID:    operationID,
	})

	var preflightCreates atomic.Int32
	fixture.client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}
		if pod, ok := create.GetObject().(*corev1.Pod); ok &&
			pod.Labels["app.kubernetes.io/component"] == "agent-upgrade-preflight" {
			preflightCreates.Add(1)
		}
		return false, nil, nil
	})

	payload, err := json.Marshal(protocol.AgentUpgradePayload{
		OperationID: operationID,
		TargetImage: testTargetImage,
	})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	msg := &protocol.Message{Type: protocol.MsgAgentUpgrade, Payload: payload}

	var (
		mu      sync.Mutex
		results []protocol.AgentUpgradeResultPayload
		wg      sync.WaitGroup
	)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := fixture.handler.HandleUpgrade(context.Background(), msg)
			if err != nil {
				t.Errorf("HandleUpgrade returned error: %v", err)
				return
			}
			if resp == nil {
				// The deduped attempt: deliberately silent so it cannot
				// complete an operation the other attempt still owns.
				return
			}
			var result protocol.AgentUpgradeResultPayload
			if err := json.Unmarshal(resp.Payload, &result); err != nil {
				t.Errorf("decode result: %v", err)
				return
			}
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(results) != 1 {
		t.Fatalf("got %d upgrade results, want exactly 1; a redelivered command must not answer for an operation another attempt owns: %+v", len(results), results)
	}
	if results[0].Phase != protocol.AgentUpgradePhaseRolloutStarted {
		t.Fatalf("result phase = %q (%s), want %q", results[0].Phase, results[0].Error, protocol.AgentUpgradePhaseRolloutStarted)
	}
	if got := preflightCreates.Load(); got != 1 {
		t.Fatalf("preflight pods created = %d, want 1; the two attempts are still racing over the same pod name", got)
	}
	if got := fixture.deployedImage(t); got != testTargetImage {
		t.Fatalf("deployment image = %q, want the rollout to have committed to %q", got, testTargetImage)
	}
}

// TestPreflightAdoptsAnInFlightPodInsteadOfDeletingIt covers the second half of
// the redelivery fix directly, for the cross-process case the in-memory guard
// cannot see (an agent restarted mid-preflight). A pod that is still pulling for
// THIS operation must be polled, never deleted: deleting it is what turned a
// live verification into "pod disappeared before the image could be verified".
func TestPreflightAdoptsAnInFlightPodInsteadOfDeletingIt(t *testing.T) {
	const operationID = "op-adopt"
	fixture := newUpgradeFixture(t, upgradeFixtureOptions{pullSucceeds: true, operationID: operationID})

	name := preflightPodName(operationID, "target", testTargetImage)
	// A pod left behind by an earlier attempt, still pulling: no container
	// status yet, phase Pending.
	if _, err := fixture.client.CoreV1().Pods(DefaultAgentNamespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   DefaultAgentNamespace,
			Labels:      map[string]string{"app.kubernetes.io/component": "agent-upgrade-preflight"},
			Annotations: map[string]string{agentUpgradeOperationAnnotation: operationID},
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "preflight", Image: testTargetImage}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed in-flight preflight pod: %v", err)
	}

	pod := preflightPod(name, DefaultAgentNamespace, testTargetImage, operationID, corev1.PodSpec{})
	got, err := fixture.handler.adoptOrCreatePreflightPod(context.Background(), DefaultAgentNamespace, name, operationID, pod)
	if err != nil {
		t.Fatalf("adoptOrCreatePreflightPod: %v", err)
	}
	if got.Name != name {
		t.Fatalf("adopted pod = %q, want %q", got.Name, name)
	}
	for _, entry := range fixture.recorder.snapshot() {
		if entry == "delete pods" {
			t.Fatalf("an in-flight preflight pod was deleted; a concurrent attempt would read NotFound and report a spurious failure: %v", fixture.recorder.snapshot())
		}
	}

	// A pod that has reached a terminal phase answers nothing for this attempt
	// and must be replaced.
	terminal := preflightPodName(operationID, "rollback", testCurrentImage)
	if _, err := fixture.client.CoreV1().Pods(DefaultAgentNamespace).Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        terminal,
			Namespace:   DefaultAgentNamespace,
			Annotations: map[string]string{agentUpgradeOperationAnnotation: operationID},
		},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
	}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed terminal preflight pod: %v", err)
	}
	if _, err := fixture.handler.adoptOrCreatePreflightPod(context.Background(), DefaultAgentNamespace, terminal, operationID,
		preflightPod(terminal, DefaultAgentNamespace, testCurrentImage, operationID, corev1.PodSpec{})); err != nil {
		t.Fatalf("adoptOrCreatePreflightPod on a terminal pod: %v", err)
	}
	if i := fixture.recorder.indexOf("delete pods"); i < 0 {
		t.Fatalf("a terminal preflight pod was adopted; its stale status would be read as this attempt's answer: %v", fixture.recorder.snapshot())
	}
}
