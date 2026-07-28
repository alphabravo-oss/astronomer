// Package agent — in-cluster self-upgrade watchdog.
//
// The agent Deployment uses strategy: Recreate (deploy/agent/install.yaml.template),
// so the moment the agent patches its own image the old pod is TERMINATED
// FIRST. The process that issued the patch therefore cannot verify the rollout
// and cannot roll it back: it is already gone. Anything that verifies a
// self-upgrade must survive the pod being replaced.
//
// That is this file. Before touching the Deployment, the agent creates a
// short-lived Job running the KNOWN-GOOD image it is currently running (the
// rollback image, which is provably pullable because it is executing) with the
// `upgrade-watchdog` subcommand. The watchdog waits for the patch to land, then
// waits for the Deployment to report an available replica on the new image, and
// if that never happens within the rollout timeout it patches the Deployment
// back to the rollback image and records why on the Deployment.
//
// The health signal is deliberately "Deployment.Status.AvailableReplicas >= 1
// on the updated ReplicaSet", NOT "the Deployment object was updated". The
// agent's readinessProbe is GET /readyz, which returns 200 only while the
// tunnel is connected (internal/agent/health.go). So an available replica means
// the REPLACEMENT AGENT STARTED AND RECONNECTED TO THE CONTROL PLANE — the
// strong evidence — expressed in a form an in-cluster watchdog can read from
// the kube-apiserver alone, with no control-plane reachability required at the
// moment of failure.
//
// If the watchdog itself dies: restartPolicy OnFailure plus the Job's
// backoffLimit restart it, and it is idempotent (it re-reads the Deployment and
// picks up where it left off). A restart resets the process's own timers while
// the Job's ActiveDeadlineSeconds keeps counting from Job start, so that
// deadline is sized for two full runs; the poll loops additionally retry failed
// reads rather than exiting, so a transient apiserver error costs a poll
// interval instead of a restart. If the Job is deleted, the node dies, or the
// kube-apiserver is unreachable for the whole window, NOTHING rolls the agent
// back and the cluster stays dark until an operator runs kubectl — that residual
// case is why the agent refuses to start a rollout until it has seen the
// watchdog pod actually Running (see self_upgrade.go), and why the server-side
// stuck-operation sweeper exists to surface it.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

const (
	// agentUpgradeStatusAnnotation carries the watchdog's terminal verdict. It
	// lives on the Deployment's OWN metadata, never on the pod template:
	// a template annotation change would trigger another Recreate rollout and
	// bounce the agent every time the watchdog wrote a status.
	agentUpgradeStatusAnnotation = "astronomer.io/agent-upgrade-status"

	upgradeWatchdogCommand = "upgrade-watchdog"

	upgradePhaseSucceeded  = "succeeded"
	upgradePhaseRolledBack = "rolled_back"
	upgradePhaseSuperseded = "superseded"
	upgradePhaseStuck      = "stuck"

	defaultUpgradeRolloutTimeout = 5 * time.Minute
	defaultUpgradePatchTimeout   = 2 * time.Minute
	defaultUpgradePollInterval   = 3 * time.Second
)

// Environment contract between the agent and the watchdog Job it creates.
const (
	envWatchdogNamespace      = "ASTRONOMER_UPGRADE_NAMESPACE"
	envWatchdogDeployment     = "ASTRONOMER_UPGRADE_DEPLOYMENT"
	envWatchdogOperationID    = "ASTRONOMER_UPGRADE_OPERATION_ID"
	envWatchdogTargetImage    = "ASTRONOMER_UPGRADE_TARGET_IMAGE"
	envWatchdogRollbackImage  = "ASTRONOMER_UPGRADE_ROLLBACK_IMAGE"
	envWatchdogRolloutTimeout = "ASTRONOMER_UPGRADE_ROLLOUT_TIMEOUT_SECONDS"
)

// UpgradeWatchdogOptions is the watchdog's whole input. It is passed through
// the Job's environment so the watchdog needs no tunnel, no credentials beyond
// its ServiceAccount, and no control-plane connectivity.
type UpgradeWatchdogOptions struct {
	Namespace      string
	Deployment     string
	OperationID    string
	TargetImage    string
	RollbackImage  string
	PatchTimeout   time.Duration
	RolloutTimeout time.Duration
	PollInterval   time.Duration
}

func (o *UpgradeWatchdogOptions) applyDefaults() {
	if strings.TrimSpace(o.Namespace) == "" {
		o.Namespace = DefaultAgentNamespace
	}
	if strings.TrimSpace(o.Deployment) == "" {
		o.Deployment = DefaultAgentDeploymentName
	}
	if o.PatchTimeout <= 0 {
		o.PatchTimeout = defaultUpgradePatchTimeout
	}
	if o.RolloutTimeout <= 0 {
		o.RolloutTimeout = defaultUpgradeRolloutTimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = defaultUpgradePollInterval
	}
}

// upgradeStatusRecord is the JSON written to agentUpgradeStatusAnnotation. The
// rolled-back agent reads it on its next connect and reports the failure to the
// control plane (upgrade_report.go), so the operator sees the kubelet's actual
// reason instead of a bare timeout.
type upgradeStatusRecord struct {
	OperationID   string `json:"operation_id"`
	Phase         string `json:"phase"`
	Error         string `json:"error,omitempty"`
	TargetImage   string `json:"target_image,omitempty"`
	RollbackImage string `json:"rollback_image,omitempty"`
	RecordedAt    string `json:"recorded_at"`
	Reported      bool   `json:"reported,omitempty"`
}

// RunUpgradeWatchdogFromEnv is the `astronomer-agent upgrade-watchdog`
// entrypoint. It builds an in-cluster client and runs the watchdog loop.
func RunUpgradeWatchdogFromEnv(ctx context.Context, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("upgrade watchdog requires in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("upgrade watchdog kubernetes client: %w", err)
	}
	opts := UpgradeWatchdogOptions{
		Namespace:     os.Getenv(envWatchdogNamespace),
		Deployment:    os.Getenv(envWatchdogDeployment),
		OperationID:   os.Getenv(envWatchdogOperationID),
		TargetImage:   os.Getenv(envWatchdogTargetImage),
		RollbackImage: os.Getenv(envWatchdogRollbackImage),
	}
	if seconds, convErr := strconv.Atoi(strings.TrimSpace(os.Getenv(envWatchdogRolloutTimeout))); convErr == nil && seconds > 0 {
		opts.RolloutTimeout = time.Duration(seconds) * time.Second
	}
	return RunUpgradeWatchdog(ctx, client, log, opts)
}

// RunUpgradeWatchdog verifies one self-upgrade and rolls it back if it never
// becomes healthy. It returns an error ONLY when it could not do its job (so
// the Job retries); a successful rollback is a successful watchdog run.
func RunUpgradeWatchdog(ctx context.Context, client kubernetes.Interface, log *slog.Logger, opts UpgradeWatchdogOptions) error {
	if log == nil {
		log = slog.Default()
	}
	opts.applyDefaults()
	if strings.TrimSpace(opts.OperationID) == "" {
		return fmt.Errorf("upgrade watchdog requires %s", envWatchdogOperationID)
	}
	if strings.TrimSpace(opts.TargetImage) == "" {
		return fmt.Errorf("upgrade watchdog requires %s", envWatchdogTargetImage)
	}
	log = log.With("operation_id", opts.OperationID, "namespace", opts.Namespace, "deployment", opts.Deployment)

	patched, err := waitForUpgradePatch(ctx, client, log, opts)
	if err != nil {
		return err
	}
	if !patched {
		// The agent never committed the rollout (rejected image, failed
		// preflight, crash before the Update). There is nothing to undo.
		log.Info("upgrade watchdog exiting: rollout never started")
		return nil
	}

	healthy, deploy, err := waitForRolloutHealth(ctx, client, log, opts)
	if err != nil {
		return err
	}
	if healthy {
		log.Info("upgrade watchdog: replacement agent is available on the target image", "target_image", opts.TargetImage)
		recordUpgradeStatus(ctx, client, log, opts, upgradeStatusRecord{Phase: upgradePhaseSucceeded})
		return nil
	}
	if deploy != nil && agentImageOf(deploy) != opts.TargetImage {
		log.Info("upgrade watchdog: deployment image changed underneath us; leaving it alone",
			"observed_image", agentImageOf(deploy))
		recordUpgradeStatus(ctx, client, log, opts, upgradeStatusRecord{Phase: upgradePhaseSuperseded})
		return nil
	}

	reason := unhealthyRolloutReason(ctx, client, deploy, opts)
	if strings.TrimSpace(opts.RollbackImage) == "" || opts.RollbackImage == opts.TargetImage {
		// Nothing safe to fall back to. Record it so the operator has the
		// reason; a manual kubectl is the only recovery.
		log.Error("upgrade watchdog: rollout failed and no distinct rollback image is available",
			"reason", reason, "rollback_image", opts.RollbackImage)
		recordUpgradeStatus(ctx, client, log, opts, upgradeStatusRecord{Phase: upgradePhaseStuck, Error: reason})
		return nil
	}

	log.Warn("upgrade watchdog: rolling the agent back", "reason", reason,
		"target_image", opts.TargetImage, "rollback_image", opts.RollbackImage)
	if err := rollbackAgentImage(ctx, client, opts, reason); err != nil {
		return fmt.Errorf("roll agent back to %s: %w", opts.RollbackImage, err)
	}
	log.Warn("upgrade watchdog: agent rolled back", "rollback_image", opts.RollbackImage)
	return nil
}

// waitForUpgradePatch blocks until the Deployment's pod template carries this
// operation's annotation, i.e. the agent actually committed the rollout. The
// watchdog is created BEFORE the patch (so it cannot be skipped by the agent
// dying mid-upgrade), which means it must tolerate a rollout that never starts.
func waitForUpgradePatch(ctx context.Context, client kubernetes.Interface, log *slog.Logger, opts UpgradeWatchdogOptions) (bool, error) {
	deadline := time.Now().Add(opts.PatchTimeout)
	var (
		seen    bool
		lastErr error
	)
	for {
		deploy, err := client.AppsV1().Deployments(opts.Namespace).Get(ctx, opts.Deployment, metav1.GetOptions{})
		switch {
		case err != nil:
			// A single transient read failure must not end the run. See
			// waitForRolloutHealth for why exiting here costs the rollback.
			lastErr = err
			log.Warn("upgrade watchdog: could not read agent deployment; retrying", "error", err)
		case deploy.Spec.Template.Annotations[agentUpgradeOperationAnnotation] == opts.OperationID:
			return true, nil
		default:
			seen, lastErr = true, nil
		}
		if !time.Now().Before(deadline) {
			if !seen && lastErr != nil {
				// Never got a single reading: we cannot tell whether a rollout
				// started, so fail and let the Job restart us rather than
				// silently declaring there is nothing to supervise.
				return false, fmt.Errorf("read agent deployment: %w", lastErr)
			}
			log.Info("upgrade watchdog: no rollout for this operation within the patch window", "patch_timeout", opts.PatchTimeout)
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
}

// waitForRolloutHealth returns once the rollout is healthy, superseded, or the
// rollout timeout expires. The returned Deployment is the last one observed.
//
// A failed Get is retried, never fatal. Exiting on the first error looks
// harmless — restartPolicy OnFailure restarts the watchdog and the run is
// idempotent — but the restart resets this loop's timeout while the Job's
// ActiveDeadlineSeconds keeps counting from Job start, so a restart late in the
// health wait lands its fresh budget past the deadline. An exceeded
// ActiveDeadline fails the Job PERMANENTLY (unlike backoffLimit), which means no
// rollback, no status annotation for the agent to report, and a cluster sitting
// dark on the bad image until the server-side sweeper emits a generic timeout.
// One blip on the apiserver is not worth that.
func waitForRolloutHealth(ctx context.Context, client kubernetes.Interface, log *slog.Logger, opts UpgradeWatchdogOptions) (bool, *appsv1.Deployment, error) {
	deadline := time.Now().Add(opts.RolloutTimeout)
	var (
		last    *appsv1.Deployment
		lastErr error
	)
	for {
		deploy, err := client.AppsV1().Deployments(opts.Namespace).Get(ctx, opts.Deployment, metav1.GetOptions{})
		if err != nil {
			lastErr = err
			log.Warn("upgrade watchdog: could not read agent deployment; retrying", "error", err)
		} else {
			last, lastErr = deploy, nil
			if agentImageOf(deploy) != opts.TargetImage {
				return false, deploy, nil
			}
			if replicasOf(deploy) == 0 {
				log.Info("upgrade watchdog: agent deployment is scaled to zero; not rolling back")
				return true, deploy, nil
			}
			if rolloutHealthy(deploy) {
				return true, deploy, nil
			}
		}
		if !time.Now().Before(deadline) {
			if last == nil {
				// Never read the Deployment once: rolling back on no evidence
				// would be guessing. Fail so the Job restarts us.
				return false, nil, fmt.Errorf("read agent deployment: %w", lastErr)
			}
			return false, last, nil
		}
		select {
		case <-ctx.Done():
			return false, last, ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
}

// rolloutHealthy is the health signal. AvailableReplicas counts pods that pass
// the readinessProbe, and the agent's readinessProbe is /readyz which is 200
// only while the tunnel is connected — so this is "the replacement agent
// reconnected", read locally.
func rolloutHealthy(deploy *appsv1.Deployment) bool {
	if deploy == nil || deploy.Status.ObservedGeneration < deploy.Generation {
		return false
	}
	desired := replicasOf(deploy)
	if desired == 0 {
		return false
	}
	return deploy.Status.UpdatedReplicas == desired &&
		deploy.Status.Replicas == desired &&
		deploy.Status.UnavailableReplicas == 0 &&
		deploy.Status.AvailableReplicas >= 1
}

func replicasOf(deploy *appsv1.Deployment) int32 {
	if deploy == nil {
		return 0
	}
	if deploy.Spec.Replicas == nil {
		return 1
	}
	return *deploy.Spec.Replicas
}

func agentImageOf(deploy *appsv1.Deployment) string {
	idx := agentContainerIndex(deploy)
	if idx < 0 {
		return ""
	}
	return deploy.Spec.Template.Spec.Containers[idx].Image
}

// unhealthyRolloutReason turns "it did not come up" into something an operator
// can act on: the kubelet's waiting reason on the new pod, or the Deployment's
// own progress condition.
func unhealthyRolloutReason(ctx context.Context, client kubernetes.Interface, deploy *appsv1.Deployment, opts UpgradeWatchdogOptions) string {
	fallback := fmt.Sprintf("replacement agent on %s did not become ready within %s", opts.TargetImage, opts.RolloutTimeout)
	if deploy == nil {
		return fallback
	}
	selector, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
	if err != nil || selector == nil || selector.Empty() {
		selector = labels.Everything()
	}
	pods, err := client.CoreV1().Pods(opts.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err == nil {
		for i := range pods.Items {
			pod := &pods.Items[i]
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
					return strings.TrimSpace(fmt.Sprintf("%s: %s %s", fallback, cs.State.Waiting.Reason, cs.State.Waiting.Message))
				}
				if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
					return strings.TrimSpace(fmt.Sprintf("%s: %s %s", fallback, cs.State.Terminated.Reason, cs.State.Terminated.Message))
				}
			}
		}
	}
	for _, cond := range deploy.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing && cond.Status == corev1.ConditionFalse {
			return strings.TrimSpace(fmt.Sprintf("%s: %s %s", fallback, cond.Reason, cond.Message))
		}
	}
	return fallback
}

// rollbackAgentImage restores the rollback image and records the verdict in a
// single Update, so a watchdog that dies immediately afterwards still leaves
// both the working agent and the reason behind.
func rollbackAgentImage(ctx context.Context, client kubernetes.Interface, opts UpgradeWatchdogOptions, reason string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := client.AppsV1().Deployments(opts.Namespace).Get(ctx, opts.Deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if agentImageOf(deploy) != opts.TargetImage {
			// Someone corrected it while we were deciding. Do not fight them.
			return nil
		}
		next := deploy.DeepCopy()
		idx := agentContainerIndex(next)
		if idx < 0 {
			return fmt.Errorf("deployment %s/%s has no containers", opts.Namespace, opts.Deployment)
		}
		next.Spec.Template.Spec.Containers[idx].Image = opts.RollbackImage
		if next.Spec.Template.Annotations == nil {
			next.Spec.Template.Annotations = map[string]string{}
		}
		// Distinct value so the rollback is its own pod-template revision even
		// when the rollback image equals the pre-upgrade image.
		next.Spec.Template.Annotations[agentUpgradeOperationAnnotation] = opts.OperationID + ":rollback"
		setUpgradeStatusAnnotation(next, upgradeStatusRecord{
			OperationID:   opts.OperationID,
			Phase:         upgradePhaseRolledBack,
			Error:         reason,
			TargetImage:   opts.TargetImage,
			RollbackImage: opts.RollbackImage,
			RecordedAt:    time.Now().UTC().Format(time.RFC3339),
		})
		_, err = client.AppsV1().Deployments(opts.Namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
}

// recordUpgradeStatus writes a non-rollback verdict. Failure to record is
// logged and swallowed: it is observability, never the rollback itself.
func recordUpgradeStatus(ctx context.Context, client kubernetes.Interface, log *slog.Logger, opts UpgradeWatchdogOptions, record upgradeStatusRecord) {
	record.OperationID = opts.OperationID
	record.TargetImage = opts.TargetImage
	record.RollbackImage = opts.RollbackImage
	record.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deploy, err := client.AppsV1().Deployments(opts.Namespace).Get(ctx, opts.Deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		next := deploy.DeepCopy()
		setUpgradeStatusAnnotation(next, record)
		_, err = client.AppsV1().Deployments(opts.Namespace).Update(ctx, next, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		log.Warn("upgrade watchdog could not record status", "phase", record.Phase, "error", err)
	}
}

func setUpgradeStatusAnnotation(deploy *appsv1.Deployment, record upgradeStatusRecord) {
	body, err := json.Marshal(record)
	if err != nil {
		return
	}
	if deploy.Annotations == nil {
		deploy.Annotations = map[string]string{}
	}
	deploy.Annotations[agentUpgradeStatusAnnotation] = string(body)
}

// watchdogJobName is deterministic per operation so a redelivered upgrade
// command reuses the existing watchdog instead of racing a second one.
func watchdogJobName(operationID string) string {
	id := strings.ToLower(strings.TrimSpace(operationID))
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, id)
	if len(id) > 24 {
		id = id[:24]
	}
	return "astronomer-agent-upgrade-" + strings.Trim(id, "-")
}

// buildWatchdogJob renders the watchdog Job. It runs the ROLLBACK image, never
// the target: the rollback image is executing right now, so it is the one image
// we know the cluster can pull — a watchdog that cannot start is worse than no
// watchdog, because the agent would have already committed the rollout.
func buildWatchdogJob(opts UpgradeWatchdogOptions, template corev1.PodSpec, serviceAccount string) *batchv1.Job {
	rolloutSeconds := int64(opts.RolloutTimeout / time.Second)
	// One run is patch window + rollout window + slack for the rollback Update.
	// The Job's deadline has to cover a RESTART of that run, not one of it:
	// restartPolicy OnFailure resets the process's own timers while
	// ActiveDeadlineSeconds keeps counting from Job start, so a deadline sized
	// for a single run kills a restarted watchdog mid-health-wait — and an
	// exceeded ActiveDeadline fails the Job permanently (unlike backoffLimit),
	// leaving the bad image in place with nothing to roll it back. Two runs plus
	// slack is the smallest budget that guarantees one complete retry, and
	// TTLSecondsAfterFinished still reaps the Job afterwards.
	runSeconds := int64(opts.PatchTimeout/time.Second) + rolloutSeconds + 120
	activeDeadline := 2*runSeconds + 60
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      watchdogJobName(opts.OperationID),
			Namespace: opts.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "astronomer-agent",
				"app.kubernetes.io/component": "agent-upgrade-watchdog",
				"app.kubernetes.io/part-of":   "astronomer",
			},
			Annotations: map[string]string{agentUpgradeOperationAnnotation: opts.OperationID},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(6)),
			ActiveDeadlineSeconds:   ptr.To(activeDeadline),
			TTLSecondsAfterFinished: ptr.To(int32(3600)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					// These are the agent Deployment's OWN pod labels. They are
					// what the shipped astronomer-agent NetworkPolicy selects,
					// so the watchdog keeps egress to the kube-apiserver on
					// clusters running an older manifest. It is not adopted by
					// the agent ReplicaSet (it has the Job as its controller)
					// and is not published by the agent Service (it exposes no
					// port named "health").
					Labels: map[string]string{
						"app.kubernetes.io/name":      "astronomer-agent",
						"app.kubernetes.io/component": "agent",
						"app.kubernetes.io/part-of":   "astronomer",
						"astronomer.io/role":          "agent-upgrade-watchdog",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: serviceAccount,
					NodeSelector:       template.NodeSelector,
					Tolerations:        template.Tolerations,
					ImagePullSecrets:   template.ImagePullSecrets,
					PriorityClassName:  template.PriorityClassName,
					SecurityContext:    template.SecurityContext,
					Containers: []corev1.Container{{
						Name: "watchdog",
						// The image we are currently running: provably pullable.
						Image: opts.RollbackImage,
						// IfNotPresent, so the watchdog starts from the node's
						// existing copy even if the registry is unreachable.
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"astronomer-agent", upgradeWatchdogCommand},
						Env: []corev1.EnvVar{
							{Name: envWatchdogNamespace, Value: opts.Namespace},
							{Name: envWatchdogDeployment, Value: opts.Deployment},
							{Name: envWatchdogOperationID, Value: opts.OperationID},
							{Name: envWatchdogTargetImage, Value: opts.TargetImage},
							{Name: envWatchdogRollbackImage, Value: opts.RollbackImage},
							{Name: envWatchdogRolloutTimeout, Value: strconv.FormatInt(rolloutSeconds, 10)},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptr.To(false),
							ReadOnlyRootFilesystem:   ptr.To(true),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
}
