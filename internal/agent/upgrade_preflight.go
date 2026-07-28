package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// preflightSentinelCommand is a path that cannot exist in any image. The
// preflight container is never meant to RUN — overriding the entrypoint with a
// missing binary means the kubelet pulls the image and then fails container
// creation, which is a positive pull signal we can read off the container
// status without executing a single byte of the candidate image.
const preflightSentinelCommand = "/astronomer-agent-image-preflight-does-not-exist"

// Waiting reasons the kubelet reports when the image could NOT be obtained.
var imagePullFailureReasons = map[string]bool{
	"ErrImagePull":              true,
	"ImagePullBackOff":          true,
	"InvalidImageName":          true,
	"ImageInspectError":         true,
	"ErrImageNeverPull":         true,
	"RegistryUnavailable":       true,
	"SignatureValidationFailed": true,
}

// Waiting reasons that can only be reached AFTER the image bytes are on the
// node — the container was created (or attempted) from a resolved image.
var imagePresentWaitingReasons = map[string]bool{
	"CreateContainerError":       true,
	"CreateContainerConfigError": true,
	"RunContainerError":          true,
	"StartError":                 true,
	"CrashLoopBackOff":           true,
	"PostStartHookError":         true,
}

// verifyImagePullable proves the cluster can obtain an image before the agent
// commits to it, by making the kubelet do the pull under the agent's own pull
// configuration (imagePullSecrets, nodeSelector, tolerations, runtime).
//
// What "verified" COVERS:
//   - the reference parses and resolves at the registry;
//   - the registry is reachable from the cluster network;
//   - the pull secrets / node credentials in effect for the agent authorize it;
//   - the manifest and layers download and unpack on a real node in this
//     cluster, with imagePullPolicy: Always so a stale node cache cannot mask a
//     deleted or moved tag.
//
// What it does NOT cover:
//   - anything about the image CONTENTS: no signature/provenance verification,
//     no SBOM, no check that it is even an astronomer-agent build. The
//     entrypoint is deliberately overridden and never executed;
//   - that the SAME node the agent later lands on can pull it — the preflight
//     pod is scheduled independently, so this proves "pullable by at least one
//     eligible node", not "pullable everywhere";
//   - that the new agent will work. Rollout health is the watchdog's job.
//
// It fails CLOSED: an inconclusive result (pod never scheduled, pull still in
// flight at the deadline) is reported as an error and the upgrade is abandoned
// with the Deployment untouched.
func (h *SelfUpgradeHandler) verifyImagePullable(ctx context.Context, namespace, image, operationID, role string, template corev1.PodSpec) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("kubernetes client is not configured")
	}
	name := preflightPodName(operationID, role, image)
	pod := preflightPod(name, namespace, image, operationID, template)

	created, err := h.adoptOrCreatePreflightPod(ctx, namespace, name, operationID, pod)
	if err != nil {
		return err
	}
	defer h.deletePreflightPod(namespace, created.Name)

	deadline := time.Now().Add(h.preflightTimeout())
	for {
		current, err := h.client.CoreV1().Pods(namespace).Get(ctx, created.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("image preflight pod disappeared before the image could be verified")
			}
			return fmt.Errorf("read image preflight pod: %w", err)
		}
		pulled, reason, decided := classifyImagePull(current)
		if decided {
			if pulled {
				h.log.Info("agent self-upgrade image verified pullable",
					"image", image, "role", role, "operation_id", operationID)
				return nil
			}
			return fmt.Errorf("image %s is not pullable in this cluster: %s", image, reason)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("image %s could not be verified within %s (last state: %s)", image, h.preflightTimeout(), describePodWait(current))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollInterval()):
		}
	}
}

// adoptOrCreatePreflightPod returns the pod whose status answers this
// verification, creating it when there is none usable.
//
// The pre-existing pod is cleared only when it CANNOT answer for us: it reached
// a terminal phase (a stale Succeeded/Failed would be read as this attempt's
// answer), it is being deleted, or it belongs to a different operation.
//
// A pod that is still pulling for the SAME operation is adopted, never deleted.
// preflightPodName is deterministic in (operationID, role, image), so an
// unconditional delete here lets one attempt destroy another's in-flight pod;
// the victim's next Get returns NotFound, it reports "pod disappeared before the
// image could be verified", and the control plane records an operation FAILED
// whose rollout is in fact proceeding. Adoption converges both attempts on one
// pod and one answer.
func (h *SelfUpgradeHandler) adoptOrCreatePreflightPod(ctx context.Context, namespace, name, operationID string, pod *corev1.Pod) (*corev1.Pod, error) {
	existing, err := h.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		if preflightPodIsLive(existing, operationID) {
			h.log.Info("agent self-upgrade adopting an in-flight image preflight pod",
				"namespace", namespace, "pod", name, "operation_id", operationID)
			return existing, nil
		}
		h.deletePreflightPod(namespace, name)
	case !apierrors.IsNotFound(err):
		return nil, fmt.Errorf("read image preflight pod: %w", err)
	}

	created, err := h.client.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create image preflight pod: %w", err)
	}
	return created, nil
}

// preflightPodIsLive reports whether pod is this operation's own preflight pod
// and has not yet reached a state that answers the question.
func preflightPodIsLive(pod *corev1.Pod, operationID string) bool {
	if pod == nil || pod.DeletionTimestamp != nil {
		return false
	}
	if pod.Annotations[agentUpgradeOperationAnnotation] != operationID {
		return false
	}
	switch pod.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return false
	}
	return true
}

// classifyImagePull turns a preflight pod's status into a decision. `decided`
// is false while the answer is still genuinely unknown (scheduling, pulling),
// which keeps the caller polling rather than guessing.
func classifyImagePull(pod *corev1.Pod) (pulled bool, reason string, decided bool) {
	if pod == nil {
		return false, "", false
	}
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Status.ContainerStatuses)+len(pod.Status.InitContainerStatuses))
	statuses = append(statuses, pod.Status.InitContainerStatuses...)
	statuses = append(statuses, pod.Status.ContainerStatuses...)
	for _, cs := range statuses {
		switch {
		case cs.State.Running != nil, cs.State.Terminated != nil:
			return true, "", true
		case cs.State.Waiting != nil:
			r := cs.State.Waiting.Reason
			if imagePullFailureReasons[r] {
				return false, strings.TrimSpace(r + ": " + cs.State.Waiting.Message), true
			}
			if imagePresentWaitingReasons[r] {
				return true, "", true
			}
		}
		// ImageID is only populated once the runtime has the image locally.
		if strings.TrimSpace(cs.ImageID) != "" {
			return true, "", true
		}
	}
	// A pod that reached a terminal phase necessarily ran (or was killed after
	// creation) — either way the image was resolved. DeadlineExceeded is the
	// exception: it can fire while the pull is still in flight.
	if pod.Status.Phase == corev1.PodSucceeded {
		return true, "", true
	}
	if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason != "DeadlineExceeded" {
		return true, "", true
	}
	return false, "", false
}

func describePodWait(pod *corev1.Pod) string {
	if pod == nil {
		return "unknown"
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			return strings.TrimSpace(string(pod.Status.Phase) + "/" + cs.State.Waiting.Reason + " " + cs.State.Waiting.Message)
		}
	}
	if reason := strings.TrimSpace(pod.Status.Reason); reason != "" {
		return string(pod.Status.Phase) + "/" + reason
	}
	return string(pod.Status.Phase)
}

// deletePreflightPod removes the throwaway pod on its own context: the caller's
// context is frequently already cancelled (the agent is being torn down by the
// very rollout it just started) and the pod must still go away.
func (h *SelfUpgradeHandler) deletePreflightPod(namespace, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := h.client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: ptr.To(int64(0)),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		h.log.Warn("agent self-upgrade could not delete image preflight pod",
			"namespace", namespace, "pod", name, "error", err)
	}
}

// preflightPodName is deterministic per (operation, role, image) so a retried
// or redelivered upgrade command reuses one pod instead of littering the
// namespace, and so the name stays inside the 63-character limit for any image.
func preflightPodName(operationID, role, image string) string {
	sum := sha256.Sum256([]byte(operationID + "|" + role + "|" + image))
	return "astronomer-agent-preflight-" + role + "-" + hex.EncodeToString(sum[:])[:12]
}

func preflightPod(name, namespace, image, operationID string, template corev1.PodSpec) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "astronomer-agent",
				"app.kubernetes.io/component": "agent-upgrade-preflight",
				"app.kubernetes.io/part-of":   "astronomer",
			},
			Annotations: map[string]string{agentUpgradeOperationAnnotation: operationID},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			// Belt to the caller's poll deadline: even if the agent is killed
			// mid-preflight by an unrelated rollout, the pod reaps itself.
			ActiveDeadlineSeconds:        ptr.To(int64(600)),
			AutomountServiceAccountToken: ptr.To(false),
			// Pull configuration must match the agent's, or a successful
			// preflight would prove nothing about the real rollout.
			ImagePullSecrets:  template.ImagePullSecrets,
			NodeSelector:      template.NodeSelector,
			Tolerations:       template.Tolerations,
			PriorityClassName: template.PriorityClassName,
			SecurityContext:   template.SecurityContext,
			Containers: []corev1.Container{{
				Name:  "preflight",
				Image: image,
				// Always, not IfNotPresent: a node that already cached this tag
				// would otherwise pass a tag that no longer exists upstream.
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{preflightSentinelCommand},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("10m"),
						corev1.ResourceMemory: resource.MustParse("16Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
				},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}
