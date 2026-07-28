package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const agentUpgradeOperationAnnotation = "astronomer.io/agent-upgrade-operation"

const (
	defaultPreflightTimeout    = 3 * time.Minute
	defaultWatchdogStartWindow = 90 * time.Second
)

// UpgradePolicy is the operator-controlled half of self-upgrade safety. It is
// built from AgentConfig at startup; the zero value is fail-closed (no
// repository configured means the handler falls back to the repository of the
// image it is currently running, and refuses everything else).
type UpgradePolicy struct {
	// AllowedRepository is an exact repository match. Empty means "derive it
	// from the running image".
	AllowedRepository string
	// AllowMutableTag permits floating tags such as :latest. Off by default:
	// a floating tag cannot be verified or meaningfully rolled back.
	AllowMutableTag bool
	// RolloutTimeout bounds how long the in-cluster watchdog waits for the
	// replacement agent to reconnect before rolling back.
	RolloutTimeout time.Duration
}

// SelfUpgradeHandler handles server-initiated agent lifecycle operations that
// affect the agent Deployment itself.
//
// Ordering is the whole design (see upgrade_watchdog.go for why the agent
// cannot verify its own rollout):
//
//  1. validate the target image reference against the allow-list — no mutation;
//  2. prove the kubelet can pull it, and can pull the rollback image too —
//     no mutation;
//  3. start the watchdog Job on the known-good image and WAIT for its pod to be
//     running — no mutation;
//  4. only then patch the Deployment, and report "rollout_started", not success.
//
// Every step before 4 is reversible by doing nothing. Step 4 is the point of no
// return, so nothing reaches it until the thing that can undo it is alive.
type SelfUpgradeHandler struct {
	client kubernetes.Interface
	log    *slog.Logger
	policy UpgradePolicy

	// inflightOps holds the operation IDs currently being executed. MsgAgentUpgrade
	// is dispatchExempt, so a redelivery ALWAYS spawns a second handler goroutine
	// (inflight.go), and the server re-claims and re-dispatches any operation
	// still `running` after 5 minutes while nothing refreshes updated_at in
	// between. The pre-commit path can legitimately exceed that window: target
	// preflight (up to 3m) + rollback preflight (up to 3m) + watchdog start (90s).
	// Two concurrent runs of the same operation would then fight over the same
	// deterministically-named preflight pod, and the loser's spurious failure
	// would mark an operation FAILED whose rollout is in fact proceeding.
	inflightMu  sync.Mutex
	inflightOps map[string]struct{}

	// Test seams. Zero means the package default.
	preflightTimeoutOverride    time.Duration
	watchdogStartWindowOverride time.Duration
	pollIntervalOverride        time.Duration
}

func NewSelfUpgradeHandler(client kubernetes.Interface, log *slog.Logger) *SelfUpgradeHandler {
	if log == nil {
		log = slog.Default()
	}
	return &SelfUpgradeHandler{client: client, log: log}
}

// errUpgradeAlreadyInFlight is returned when a redelivered AGENT_UPGRADE names
// an operation this process is already executing.
var errUpgradeAlreadyInFlight = errors.New("agent upgrade for this operation is already in progress")

// beginUpgradeOperation claims exclusive execution of operationID, returning a
// release func and whether the claim succeeded.
func (h *SelfUpgradeHandler) beginUpgradeOperation(operationID string) (func(), bool) {
	h.inflightMu.Lock()
	defer h.inflightMu.Unlock()
	if h.inflightOps == nil {
		h.inflightOps = make(map[string]struct{})
	}
	if _, busy := h.inflightOps[operationID]; busy {
		return func() {}, false
	}
	h.inflightOps[operationID] = struct{}{}
	return func() {
		h.inflightMu.Lock()
		delete(h.inflightOps, operationID)
		h.inflightMu.Unlock()
	}, true
}

// SetUpgradePolicy installs the operator-configured image allow-list. Called
// once at startup from cmd/agent.
func (h *SelfUpgradeHandler) SetUpgradePolicy(policy UpgradePolicy) {
	if h == nil {
		return
	}
	h.policy = policy
}

func (h *SelfUpgradeHandler) preflightTimeout() time.Duration {
	if h.preflightTimeoutOverride > 0 {
		return h.preflightTimeoutOverride
	}
	return defaultPreflightTimeout
}

func (h *SelfUpgradeHandler) watchdogStartWindow() time.Duration {
	if h.watchdogStartWindowOverride > 0 {
		return h.watchdogStartWindowOverride
	}
	return defaultWatchdogStartWindow
}

func (h *SelfUpgradeHandler) pollInterval() time.Duration {
	if h.pollIntervalOverride > 0 {
		return h.pollIntervalOverride
	}
	return defaultUpgradePollInterval
}

func (h *SelfUpgradeHandler) rolloutTimeout(payload protocol.AgentUpgradePayload) time.Duration {
	if payload.RolloutTimeoutSeconds > 0 {
		return time.Duration(payload.RolloutTimeoutSeconds) * time.Second
	}
	if h.policy.RolloutTimeout > 0 {
		return h.policy.RolloutTimeout
	}
	return defaultUpgradeRolloutTimeout
}

func (h *SelfUpgradeHandler) HandleUpgrade(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var payload protocol.AgentUpgradePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil, fmt.Errorf("decode agent upgrade payload: %w", err)
	}
	result := protocol.AgentUpgradeResultPayload{
		OperationID: payload.OperationID,
		ClusterID:   payload.ClusterID,
	}
	outcome, err := h.startAgentUpgrade(ctx, payload)
	if errors.Is(err, errUpgradeAlreadyInFlight) {
		// Deliberately NO result frame. The first attempt still owns this
		// operation and will report its own outcome; answering here would
		// either race it or, if it answered rejected, complete an operation
		// whose rollout is still proceeding.
		h.log.Info("ignoring redelivered agent upgrade command; the operation is already in progress",
			"operation_id", payload.OperationID)
		return nil, nil
	}
	if err != nil {
		// Nothing was committed: the Deployment is untouched and the running
		// agent is still the one the control plane is talking to.
		result.Success = false
		result.Phase = protocol.AgentUpgradePhaseRejected
		result.Error = err.Error()
	} else {
		// Success=true keeps pre-hardening servers behaving exactly as they do
		// today; Phase is what a current server keys off to leave the operation
		// running until the replacement agent reconnects. See
		// protocol.AgentUpgradeResultPayload.
		result.Success = true
		result.Phase = protocol.AgentUpgradePhaseRolloutStarted
		result.Message = "agent deployment rollout started; awaiting replacement agent reconnect"
		result.ObservedImage = outcome.targetImage
		result.RollbackImage = outcome.rollbackImage
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode agent upgrade result: %w", err)
	}
	return &protocol.Message{
		Type:      protocol.MsgAgentUpgradeResult,
		ClusterID: payload.ClusterID,
		Timestamp: metav1.Now().Time,
		Payload:   body,
	}, nil
}

type upgradeOutcome struct {
	targetImage   string
	rollbackImage string
}

func (h *SelfUpgradeHandler) startAgentUpgrade(ctx context.Context, payload protocol.AgentUpgradePayload) (upgradeOutcome, error) {
	var out upgradeOutcome
	if h == nil || h.client == nil {
		return out, fmt.Errorf("kubernetes client is not configured")
	}
	if strings.TrimSpace(payload.OperationID) == "" {
		return out, fmt.Errorf("operation_id is required")
	}
	releaseOperation, claimed := h.beginUpgradeOperation(payload.OperationID)
	if !claimed {
		return out, errUpgradeAlreadyInFlight
	}
	defer releaseOperation()

	targetImage := strings.TrimSpace(payload.TargetImage)
	if targetImage == "" {
		return out, fmt.Errorf("target_image is required")
	}
	namespace := cmp.Or(strings.TrimSpace(payload.AgentNamespace), DefaultAgentNamespace)
	deploymentName := cmp.Or(strings.TrimSpace(payload.AgentDeployment), DefaultAgentDeploymentName)

	deploy, err := h.client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return out, fmt.Errorf("read agent deployment %s/%s: %w", namespace, deploymentName, err)
	}
	if agentContainerIndex(deploy) < 0 {
		return out, fmt.Errorf("deployment %s/%s has no containers", namespace, deploymentName)
	}
	// A command redelivered for an operation the watchdog already gave a
	// terminal verdict on must NOT re-attempt the rollout. The server re-claims
	// operations stuck in `running` every 5 minutes, so without this an upgrade
	// that fails for a reason preflight cannot see (a crash-looping new agent)
	// would be retried — and each retry takes the cluster dark for the rollout
	// window — until the stuck-operation sweeper eventually gave up.
	if verdict, ok := terminalUpgradeVerdict(deploy, payload.OperationID); ok {
		return out, fmt.Errorf("upgrade to %s already failed on this cluster and was rolled back: %s", targetImage, verdict)
	}
	currentImage := strings.TrimSpace(agentImageOf(deploy))

	// Step 1 — allow-list. The default allow-list is the repository of the
	// image we are running: that repository is trusted by construction, it
	// keeps already-deployed manifests (which carry no
	// ASTRONOMER_AGENT_IMAGE_REPOSITORY) upgradable, and it makes a move to a
	// different registry an explicit operator decision instead of an implicit
	// one available to whoever can queue an upgrade.
	policy := imagePolicy{
		AllowedRepository: cmp.Or(strings.TrimSpace(h.policy.AllowedRepository), imageRepositoryOf(currentImage)),
		AllowMutableTag:   h.policy.AllowMutableTag,
	}
	if _, err := validateAgentImage(targetImage, policy); err != nil {
		return out, fmt.Errorf("rejected target image: %w", err)
	}

	rollbackImage := strings.TrimSpace(payload.RollbackImage)
	if rollbackImage == "" {
		rollbackImage = currentImage
	} else if rollbackImage != currentImage {
		if _, err := validateAgentImage(rollbackImage, policy); err != nil {
			return out, fmt.Errorf("rejected rollback image: %w", err)
		}
	}
	if rollbackImage == "" {
		return out, fmt.Errorf("no rollback image is available; refusing to roll out %s with no way back", targetImage)
	}
	out.targetImage = targetImage
	out.rollbackImage = rollbackImage

	// Step 2 — pull verification. Rolling back to an image the cluster cannot
	// pull is a permanent outage, so the rollback image is verified too
	// whenever it is not the image already running here.
	template := deploy.Spec.Template.Spec
	if err := h.verifyImagePullable(ctx, namespace, targetImage, payload.OperationID, "target", template); err != nil {
		return out, err
	}
	if rollbackImage != currentImage {
		if err := h.verifyImagePullable(ctx, namespace, rollbackImage, payload.OperationID, "rollback", template); err != nil {
			return out, fmt.Errorf("rollback image is not usable: %w", err)
		}
	}

	// Step 3 — the watchdog, alive BEFORE the point of no return.
	opts := UpgradeWatchdogOptions{
		Namespace:      namespace,
		Deployment:     deploymentName,
		OperationID:    payload.OperationID,
		TargetImage:    targetImage,
		RollbackImage:  rollbackImage,
		RolloutTimeout: h.rolloutTimeout(payload),
	}
	opts.applyDefaults()
	if err := h.ensureWatchdogRunning(ctx, opts, deploy); err != nil {
		return out, err
	}

	// Step 4 — commit.
	if err := h.patchAgentDeployment(ctx, namespace, deploymentName, targetImage, payload.OperationID); err != nil {
		return out, err
	}
	h.log.Info("agent self-upgrade rollout started",
		"namespace", namespace,
		"deployment", deploymentName,
		"target_image", targetImage,
		"rollback_image", rollbackImage,
		"rollout_timeout", opts.RolloutTimeout,
		"operation_id", payload.OperationID,
	)
	return out, nil
}

// ensureWatchdogRunning creates the watchdog Job (idempotently — the name is
// derived from the operation ID) and blocks until one of its pods is actually
// Running. A watchdog that cannot be scheduled or cannot pull is a refusal, not
// a warning: without it a failed rollout has nothing to undo it.
func (h *SelfUpgradeHandler) ensureWatchdogRunning(ctx context.Context, opts UpgradeWatchdogOptions, deploy *appsv1.Deployment) error {
	job := buildWatchdogJob(opts, deploy.Spec.Template.Spec, deploy.Spec.Template.Spec.ServiceAccountName)
	created, err := h.client.BatchV1().Jobs(opts.Namespace).Create(ctx, job, metav1.CreateOptions{})
	switch {
	case apierrors.IsAlreadyExists(err):
		// A redelivered upgrade command for the same operation.
		existing, getErr := h.client.BatchV1().Jobs(opts.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("read existing upgrade watchdog job: %w", getErr)
		}
		if !watchdogJobFinished(existing) {
			// Still supervising; do not race a second watchdog against it.
			created = existing
			break
		}
		// A watchdog from an earlier attempt has already exited and cannot
		// supervise a new rollout. Replace it rather than committing behind a
		// corpse. Foreground propagation so its pods are gone before the name
		// is free again.
		if err := h.replaceFinishedWatchdogJob(ctx, opts.Namespace, job.Name); err != nil {
			return err
		}
		created, err = h.client.BatchV1().Jobs(opts.Namespace).Create(ctx, job, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("recreate upgrade watchdog job: %w", err)
		}
	case err != nil:
		return fmt.Errorf("create upgrade watchdog job: %w", err)
	}

	selector := labels.SelectorFromSet(labels.Set{"job-name": created.Name}).String()
	deadline := time.Now().Add(h.watchdogStartWindow())
	for {
		pods, listErr := h.client.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if listErr != nil {
			return fmt.Errorf("list upgrade watchdog pods: %w", listErr)
		}
		for i := range pods.Items {
			// Running only. A Succeeded pod is a watchdog that has already
			// finished watching, which is no protection at all.
			if pods.Items[i].Status.Phase == corev1.PodRunning {
				h.log.Info("agent self-upgrade watchdog is running",
					"job", created.Name, "pod", pods.Items[i].Name)
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("upgrade watchdog job %s did not start within %s; refusing to roll out %s without a rollback path",
				created.Name, h.watchdogStartWindow(), opts.TargetImage)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollInterval()):
		}
	}
}

func watchdogJobFinished(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	if job.Status.CompletionTime != nil || job.Status.Succeeded > 0 {
		return true
	}
	for _, cond := range job.Status.Conditions {
		if (cond.Type == batchv1.JobComplete || cond.Type == batchv1.JobFailed) && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// replaceFinishedWatchdogJob deletes a spent watchdog and waits for the name to
// be free, so the replacement Create cannot lose to a pending deletion.
func (h *SelfUpgradeHandler) replaceFinishedWatchdogJob(ctx context.Context, namespace, name string) error {
	policy := metav1.DeletePropagationForeground
	err := h.client.BatchV1().Jobs(namespace).Delete(ctx, name, metav1.DeleteOptions{PropagationPolicy: &policy})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete finished upgrade watchdog job: %w", err)
	}
	deadline := time.Now().Add(h.watchdogStartWindow())
	for {
		if _, err := h.client.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return fmt.Errorf("await upgrade watchdog job deletion: %w", err)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("finished upgrade watchdog job %s did not go away within %s", name, h.watchdogStartWindow())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollInterval()):
		}
	}
}

func (h *SelfUpgradeHandler) patchAgentDeployment(ctx context.Context, namespace, deploymentName, targetImage, operationID string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := h.client.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		next := deploy.DeepCopy()
		containerIndex := agentContainerIndex(next)
		if containerIndex < 0 {
			return fmt.Errorf("deployment %s/%s has no containers", namespace, deploymentName)
		}
		next.Spec.Template.Spec.Containers[containerIndex].Image = targetImage
		if next.Spec.Template.Annotations == nil {
			next.Spec.Template.Annotations = map[string]string{}
		}
		next.Spec.Template.Annotations[agentUpgradeOperationAnnotation] = operationID
		// Clear any verdict from a previous operation so the replacement agent
		// does not report a stale rollback as this operation's outcome.
		delete(next.Annotations, agentUpgradeStatusAnnotation)
		if _, err := h.client.AppsV1().Deployments(namespace).Update(ctx, next, metav1.UpdateOptions{}); err != nil {
			return err
		}
		return nil
	})
}

func agentContainerIndex(deploy *appsv1.Deployment) int {
	if deploy == nil {
		return -1
	}
	for i, container := range deploy.Spec.Template.Spec.Containers {
		if container.Name == "agent" {
			return i
		}
	}
	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		return 0
	}
	return -1
}
