package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const maxCharlieArgoValuesBytes = 256 << 10

// ModeCeilingInstaller owns the Kubernetes/Argo rollout gate that must close
// before the local Product Bridge is allowed to synchronize Charlie central.
type ModeCeilingInstaller interface {
	ReconcileModeCeiling(context.Context, AgentInstallSpec, Mode) error
}

type modeCeilingRollout struct {
	load      func(context.Context) (sqlc.CharlieConnection, error)
	installer ModeCeilingInstaller
}

func (r modeCeilingRollout) Reconcile(ctx context.Context, target ModeCeilingTarget) error {
	if r.load == nil || r.installer == nil || target.ConnectionID == "" || !validMode(target.ExpectedRequested) || !validMode(target.Desired) || target.ExpectedRevision < 0 {
		return fmt.Errorf("Charlie mode-ceiling rollout is unavailable")
	}
	connection, err := r.load(ctx)
	if err != nil || !modeCeilingSnapshotMatches(connection, target) {
		return fmt.Errorf("Charlie mode-ceiling rollout state changed")
	}
	if err := r.installer.ReconcileModeCeiling(ctx, adminInstallSpec(connection), target.Desired); err != nil {
		return err
	}
	confirmed, err := r.load(ctx)
	if err != nil || !modeCeilingSnapshotMatches(confirmed, target) {
		return fmt.Errorf("Charlie mode-ceiling rollout state changed")
	}
	return nil
}

func modeCeilingSnapshotMatches(connection sqlc.CharlieConnection, target ModeCeilingTarget) bool {
	return connection.ID.String() == target.ConnectionID && Mode(connection.RequestedMode) == target.ExpectedRequested &&
		connection.VerifiedModeRevision == target.ExpectedRevision && connection.Active &&
		connection.EmergencyDisabled == target.ExpectedEmergencyDisabled
}

// ReconcileModeCeiling mutates only the existing owner-bound Argo values. It
// deliberately does not regenerate installation values from incomplete DB
// metadata and never uses pruning for an authority transition.
func (i *AgentInstaller) ReconcileModeCeiling(ctx context.Context, spec AgentInstallSpec, desired Mode) error {
	if i == nil || i.kube == nil || i.dynamic == nil || spec.InstallationID.String() == "" || spec.ReplicaCount != 2 || !validMode(desired) {
		return fmt.Errorf("Charlie mode-ceiling rollout dependencies are unavailable")
	}
	names := agentResourceNames(spec, i.agentNamespace)
	resources := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace)
	application, err := resources.Get(ctx, names.Application, metav1.GetOptions{})
	if err != nil || application.GetLabels()[installationOwnerLabel] != spec.InstallationID.String() {
		return fmt.Errorf("Charlie mode-ceiling Argo Application is unavailable")
	}
	valuesText, found, err := unstructured.NestedString(application.Object, "spec", "source", "helm", "values")
	if err != nil || !found || len(valuesText) == 0 || len(valuesText) > maxCharlieArgoValuesBytes {
		return fmt.Errorf("Charlie mode-ceiling Helm values are unavailable")
	}
	var values map[string]any
	decoder := json.NewDecoder(strings.NewReader(valuesText))
	decoder.UseNumber()
	if decoder.Decode(&values) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("Charlie mode-ceiling Helm values are invalid")
	}
	runtimeValues, ok := values["runtime"].(map[string]any)
	if !ok || runtimeValues["enabled"] != true || exactJSONInteger(values["replicaCount"]) != int64(spec.ReplicaCount) {
		return fmt.Errorf("Charlie mode-ceiling Helm values are not runtime-enabled for two replicas")
	}
	runtimeValues["modeCeiling"] = string(desired)
	encoded, err := json.Marshal(values)
	if err != nil || len(encoded) > maxCharlieArgoValuesBytes {
		return fmt.Errorf("Charlie mode-ceiling Helm values are invalid")
	}
	if err = unstructured.SetNestedField(application.Object, string(encoded), "spec", "source", "helm", "values"); err != nil {
		return fmt.Errorf("Charlie mode-ceiling Helm values are invalid")
	}
	if err = unstructured.SetNestedField(application.Object, false, "spec", "syncPolicy", "automated", "prune"); err != nil {
		return fmt.Errorf("Charlie mode-ceiling non-pruning sync is unavailable")
	}
	if err = unstructured.SetNestedField(application.Object, true, "spec", "syncPolicy", "automated", "selfHeal"); err != nil {
		return fmt.Errorf("Charlie mode-ceiling self-healing sync is unavailable")
	}
	annotations := application.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["argocd.argoproj.io/refresh"] = "hard"
	annotations["astronomer.io/charlie-mode-ceiling"] = string(desired)
	application.SetAnnotations(annotations)
	if _, err = resources.Update(ctx, application, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("Charlie mode-ceiling Argo reconciliation failed")
	}
	return i.waitModeCeiling(ctx, spec, desired)
}

func exactJSONInteger(value any) int64 {
	number, ok := value.(json.Number)
	if !ok {
		return -1
	}
	integer, err := number.Int64()
	if err != nil {
		return -1
	}
	return integer
}

func (i *AgentInstaller) waitModeCeiling(ctx context.Context, spec AgentInstallSpec, desired Mode) error {
	ticker := time.NewTicker(i.pollInterval)
	defer ticker.Stop()
	for {
		if i.modeCeilingReady(ctx, spec, desired) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Charlie mode-ceiling rollout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (i *AgentInstaller) modeCeilingReady(ctx context.Context, spec AgentInstallSpec, desired Mode) bool {
	names := agentResourceNames(spec, i.agentNamespace)
	application, err := i.dynamic.Resource(kubeutil.ArgoApplicationGVR).Namespace(i.argoNamespace).Get(ctx, names.Application, metav1.GetOptions{})
	if err != nil || application.GetLabels()[installationOwnerLabel] != spec.InstallationID.String() || application.GetAnnotations()["astronomer.io/charlie-mode-ceiling"] != string(desired) {
		return false
	}
	syncStatus, _, _ := unstructured.NestedString(application.Object, "status", "sync", "status")
	healthStatus, _, _ := unstructured.NestedString(application.Object, "status", "health", "status")
	prune, pruneFound, _ := unstructured.NestedBool(application.Object, "spec", "syncPolicy", "automated", "prune")
	if syncStatus != "Synced" || healthStatus != "Healthy" || !pruneFound || prune {
		return false
	}
	statefulSet, err := i.kube.AppsV1().StatefulSets(i.agentNamespace).Get(ctx, charlieAgentWorkloadName, metav1.GetOptions{})
	if err != nil || statefulSet.Spec.Replicas == nil || *statefulSet.Spec.Replicas != 2 || statefulSet.Generation < 1 || statefulSet.Status.ObservedGeneration < statefulSet.Generation ||
		statefulSet.Status.UpdatedReplicas != 2 || statefulSet.Status.ReadyReplicas != 2 || statefulSet.Status.CurrentReplicas != 2 || statefulSet.Status.CurrentRevision == "" || statefulSet.Status.CurrentRevision != statefulSet.Status.UpdateRevision ||
		len(statefulSet.Spec.Template.Spec.Containers) != 1 || containerModeCeiling(statefulSet.Spec.Template.Spec.Containers[0]) != desired {
		return false
	}
	pods, err := i.kube.CoreV1().Pods(i.agentNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=" + charlieAgentWorkloadName + ",app.kubernetes.io/component=product-agent"})
	if err != nil || len(pods.Items) != 2 {
		return false
	}
	expectedPods := map[string]bool{charlieAgentWorkloadName + "-0": false, charlieAgentWorkloadName + "-1": false}
	for index := range pods.Items {
		pod := &pods.Items[index]
		if _, expected := expectedPods[pod.Name]; !expected || !podReady(pod) || len(pod.Spec.Containers) != 1 || containerModeCeiling(pod.Spec.Containers[0]) != desired {
			return false
		}
		owned := false
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "StatefulSet" && owner.Name == statefulSet.Name && owner.UID == statefulSet.UID {
				owned = true
			}
		}
		if !owned || expectedPods[pod.Name] {
			return false
		}
		expectedPods[pod.Name] = true
	}
	return expectedPods[charlieAgentWorkloadName+"-0"] && expectedPods[charlieAgentWorkloadName+"-1"]
}

func applicationModeCeiling(application *unstructured.Unstructured) Mode {
	if application == nil {
		return ""
	}
	valuesText, found, err := unstructured.NestedString(application.Object, "spec", "source", "helm", "values")
	if err != nil || !found || len(valuesText) == 0 || len(valuesText) > maxCharlieArgoValuesBytes {
		return ""
	}
	var values struct {
		Runtime struct {
			ModeCeiling Mode `json:"modeCeiling"`
		} `json:"runtime"`
	}
	// The full values object intentionally has other fields, so decode through a
	// bounded generic map after checking only the exact nested scalar.
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(valuesText), &raw) != nil || json.Unmarshal(raw["runtime"], &values.Runtime) != nil || !validMode(values.Runtime.ModeCeiling) {
		return ""
	}
	return values.Runtime.ModeCeiling
}

func containerModeCeiling(container corev1.Container) Mode {
	for _, variable := range container.Env {
		if variable.Name == "CHARLIE_MODE" && variable.ValueFrom == nil {
			return Mode(variable.Value)
		}
	}
	return ""
}

func podReady(pod *corev1.Pod) bool {
	if pod == nil || pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
