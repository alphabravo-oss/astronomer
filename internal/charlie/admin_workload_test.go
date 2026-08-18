package charlie

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

func TestKubernetesAgentWorkloadReadsStatefulSetReadyCount(t *testing.T) {
	client := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: agentReleaseName, Namespace: agentNamespaceName},
		Spec: appsv1.StatefulSetSpec{
			Replicas: ptr.To(int32(2)),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "agent", Env: []corev1.EnvVar{{Name: "CHARLIE_MODE", Value: "disabled"}},
			}}}},
		},
		Status: appsv1.StatefulSetStatus{ReadyReplicas: 2},
	})
	got, err := NewKubernetesAgentWorkload(client).AgentWorkload(context.Background())
	if err != nil || !got.Present || got.Desired != 2 || got.Ready != 2 || got.ModeCeiling != ModeDisabled {
		t.Fatalf("workload = %+v err=%v", got, err)
	}
}
