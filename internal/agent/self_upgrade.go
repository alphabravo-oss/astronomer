package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const agentUpgradeOperationAnnotation = "astronomer.io/agent-upgrade-operation"

// SelfUpgradeHandler handles server-initiated agent lifecycle operations that
// affect the agent Deployment itself.
type SelfUpgradeHandler struct {
	client kubernetes.Interface
	log    *slog.Logger
}

func NewSelfUpgradeHandler(client kubernetes.Interface, log *slog.Logger) *SelfUpgradeHandler {
	if log == nil {
		log = slog.Default()
	}
	return &SelfUpgradeHandler{client: client, log: log}
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
	observed, err := h.patchAgentDeployment(ctx, payload)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
	} else {
		result.Success = true
		result.Message = "agent deployment image patched"
		result.ObservedImage = observed
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

func (h *SelfUpgradeHandler) patchAgentDeployment(ctx context.Context, payload protocol.AgentUpgradePayload) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("kubernetes client is not configured")
	}
	if strings.TrimSpace(payload.OperationID) == "" {
		return "", fmt.Errorf("operation_id is required")
	}
	targetImage := strings.TrimSpace(payload.TargetImage)
	if targetImage == "" {
		return "", fmt.Errorf("target_image is required")
	}
	namespace := cmp.Or(strings.TrimSpace(payload.AgentNamespace), DefaultAgentNamespace)
	deploymentName := cmp.Or(strings.TrimSpace(payload.AgentDeployment), DefaultAgentDeploymentName)

	var observed string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
		observed = targetImage
		if next.Spec.Template.Annotations == nil {
			next.Spec.Template.Annotations = map[string]string{}
		}
		next.Spec.Template.Annotations[agentUpgradeOperationAnnotation] = payload.OperationID
		if _, err := h.client.AppsV1().Deployments(namespace).Update(ctx, next, metav1.UpdateOptions{}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	h.log.Info("agent self-upgrade deployment patched",
		"namespace", namespace,
		"deployment", deploymentName,
		"target_image", targetImage,
		"operation_id", payload.OperationID,
	)
	return observed, nil
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
