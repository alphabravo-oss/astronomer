package observation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProjectionPreservesNeededFieldsWithoutCredentialPayloads(t *testing.T) {
	replicas := int32(2)
	input := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "controller", Namespace: "system", ResourceVersion: "7", Labels: map[string]string{"app.kubernetes.io/version": "1"}, Annotations: map[string]string{"private": "DO_NOT_RETAIN"}}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: "controller@sha256:abc", Args: []string{"--no-cross-namespace-refs=true", "--token=DO_NOT_RETAIN"}, Env: []corev1.EnvVar{{Name: "SECRET", Value: "DO_NOT_RETAIN"}}, Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}}}}}}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1, ObservedGeneration: 3}}
	original := input.DeepCopy()
	raw, err := Project(input)
	if err != nil {
		t.Fatal(err)
	}
	got := raw.(*appsv1.Deployment)
	if !reflect.DeepEqual(input, original) {
		t.Fatal("transform mutated input")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "DO_NOT_RETAIN") {
		t.Fatal("projection retained credential-shaped payload")
	}
	if got.ResourceVersion != "7" || got.Status.ReadyReplicas != 1 || *got.Spec.Replicas != 2 || len(got.Spec.Template.Spec.Containers[0].Args) != 1 {
		t.Fatal("needed readiness/controller metadata lost")
	}
	got.Labels["app.kubernetes.io/version"] = "changed"
	*got.Spec.Replicas = 99
	got.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("9")
	if !reflect.DeepEqual(input, original) {
		t.Fatal("projection aliases original fields")
	}
	class := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "local", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true", "private": "DO_NOT_RETAIN"}}, Provisioner: "driver", Parameters: map[string]string{"secret": "DO_NOT_RETAIN"}}
	projected, err := Project(class)
	if err != nil {
		t.Fatal(err)
	}
	out := projected.(*storagev1.StorageClass)
	if out.Provisioner != "driver" || out.Annotations["storageclass.kubernetes.io/is-default-class"] != "true" || len(out.Parameters) != 0 || len(out.Annotations) != 1 {
		t.Fatal("storage class projection contract")
	}
	if _, err := Project(&corev1.Secret{}); err == nil {
		t.Fatal("secret accepted as observation source")
	}
}
