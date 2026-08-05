package charlie

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func managementAdapterFixture(t *testing.T, objects ...runtime.Object) *ManagementKubernetesAdapter {
	t.Helper()
	adapter, err := NewManagementKubernetesAdapter(fake.NewClientset(objects...), "astronomer", "astronomer")
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func ownedDeployment(name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "secret": "SENTINEL"}},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "server", Image: "registry.invalid/private:tag", Env: []corev1.EnvVar{{Name: "API_KEY", Value: "SENTINEL"}}}}}}},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: replicas, AvailableReplicas: replicas, UpdatedReplicas: replicas},
	}
}

func TestManagementKubernetesReadsOnlyOwnedRedactedShapes(t *testing.T) {
	adapter := managementAdapterFixture(t,
		ownedDeployment("astronomer-server", 2),
		ownedDeployment("other-server", 2),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "credential": "SENTINEL"}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)}}}},
	)
	for _, capabilityName := range []string{"astronomer.management.workloads", "astronomer.management.network"} {
		descriptor, _ := capabilityByName(capabilityName)
		result, err := adapter.Execute(context.Background(), descriptor, map[string]json.RawMessage{})
		if err != nil {
			t.Fatalf("%s: %v", capabilityName, err)
		}
		serialized := string(result)
		if strings.Contains(serialized, "SENTINEL") || strings.Contains(serialized, "registry.invalid") || strings.Contains(serialized, "other-server") {
			t.Fatalf("%s leaked an unowned or sensitive field: %s", capabilityName, serialized)
		}
	}
}

func TestManagementKubernetesWritesStayAllowlistedAndVerify(t *testing.T) {
	adapter := managementAdapterFixture(t, ownedDeployment("astronomer-server", 2), ownedDeployment("astronomer-database", 3))
	descriptor, _ := capabilityByName("astronomer.management.workload_scale")
	args := rawArguments(t, map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-server", "replicas": 4, "operation_id": "action-a"})
	result, err := adapter.Execute(context.Background(), descriptor, args)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, args, result)
	if err != nil || !verified {
		t.Fatalf("scale verification = %v, %v", verified, err)
	}

	args = rawArguments(t, map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-database", "replicas": 4, "operation_id": "action-b"})
	if _, err := adapter.Execute(context.Background(), descriptor, args); err == nil {
		t.Fatal("non-allowlisted deployment was mutable")
	}
}

func TestManagementKubernetesRolloutRequiresRedundancyAndExactReceipt(t *testing.T) {
	adapter := managementAdapterFixture(t, ownedDeployment("astronomer-worker", 1), ownedDeployment("astronomer-server", 2))
	descriptor, _ := capabilityByName("astronomer.management.workload_rollout")
	unsafeArgs := rawArguments(t, map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-worker", "operation_id": "action-a"})
	if _, err := adapter.Execute(context.Background(), descriptor, unsafeArgs); err == nil {
		t.Fatal("single-replica rollout was allowed")
	}
	safeArgs := rawArguments(t, map[string]any{"resource_id": "resource-a", "workload": "deployment/astronomer-server", "operation_id": "action-b"})
	result, err := adapter.Execute(context.Background(), descriptor, safeArgs)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, safeArgs, result)
	if err != nil || !verified {
		t.Fatalf("rollout verification = %v, %v", verified, err)
	}
}

func TestManagementKubernetesRunJobClonesOnlyFixedCronJobTemplate(t *testing.T) {
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "astronomer-restore-drill", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer"}},
		Spec:       batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "drill", Image: "fixed@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Command: []string{"/fixed"}}}}}}}},
	}
	adapter := managementAdapterFixture(t, cronJob)
	descriptor, _ := capabilityByName("astronomer.management.run_job")
	args := rawArguments(t, map[string]any{"resource_id": "resource-a", "job": "restore-drill", "operation_id": "action-a"})
	result, err := adapter.Execute(context.Background(), descriptor, args)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, args, result)
	if err != nil || !verified {
		t.Fatalf("job verification = %v, %v; result=%s", verified, err, result)
	}
}

func TestCapabilitySchemasAreExactAndComplete(t *testing.T) {
	for _, descriptor := range append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...) {
		fields := capabilityFieldSchemas(descriptor.Name)
		if len(fields) != len(descriptor.AcceptedFields) {
			t.Fatalf("%s schema fields=%d accepted=%d", descriptor.Name, len(fields), len(descriptor.AcceptedFields))
		}
		for _, field := range descriptor.AcceptedFields {
			if _, ok := fields[field]; !ok {
				t.Fatalf("%s lacks schema for %s", descriptor.Name, field)
			}
		}
	}
}

func rawArguments(t *testing.T, value map[string]any) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
