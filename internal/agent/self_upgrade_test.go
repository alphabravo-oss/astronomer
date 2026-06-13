package agent

import (
	"context"
	"encoding/json"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestSelfUpgradeHandlerPatchesAgentDeploymentImage(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DefaultAgentDeploymentName,
			Namespace: DefaultAgentNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "agent", Image: "example.com/astronomer-agent:v1.0.0"},
					},
				},
			},
		},
	})
	handler := NewSelfUpgradeHandler(client, nil)
	payload := protocol.AgentUpgradePayload{
		OperationID:   "op-1",
		ClusterID:     "cluster-1",
		TargetVersion: "v1.2.3",
		TargetImage:   "example.com/astronomer-agent:v1.2.3",
	}
	body, _ := json.Marshal(payload)

	resp, err := handler.HandleUpgrade(context.Background(), &protocol.Message{
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
	if !result.Success || result.ObservedImage != payload.TargetImage {
		t.Fatalf("result = %+v", result)
	}

	deploy, err := client.AppsV1().Deployments(DefaultAgentNamespace).Get(context.Background(), DefaultAgentDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if got := deploy.Spec.Template.Spec.Containers[0].Image; got != payload.TargetImage {
		t.Fatalf("image = %q, want %q", got, payload.TargetImage)
	}
	if got := deploy.Spec.Template.Annotations[agentUpgradeOperationAnnotation]; got != payload.OperationID {
		t.Fatalf("operation annotation = %q, want %q", got, payload.OperationID)
	}
}
