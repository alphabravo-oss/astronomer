package delivery

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func sourceControllerWarmStandbyReady(item *appsv1.Deployment) bool {
	if item == nil || item.Name != "source-controller" || item.Namespace != DeliverySystemNamespace || item.DeletionTimestamp != nil {
		return false
	}
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	if desired < 1 || item.Generation < 1 || item.Status.ObservedGeneration != item.Generation ||
		item.Status.UpdatedReplicas != desired || item.Status.Replicas != desired ||
		item.Status.AvailableReplicas < 1 || item.Status.ReadyReplicas < 1 {
		return false
	}
	for _, condition := range item.Status.Conditions {
		if condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue {
			return false
		}
	}
	return true
}

// Flux v2.9.6 evaluates these before its built-in Deployment status reader.
// The exception is limited to the artifact-serving leader and its intentional
// warm standby. All replicas must belong to the observed generation, so an old
// available leader cannot make a failed upgrade look complete. Other system
// Deployments still require every desired replica to be ready and available.
// Keep these predicates aligned with sourceControllerWarmStandbyReady; tests
// evaluate the emitted CEL and the Go readiness check against the same states.
func systemDeploymentHealthChecks() []any {
	source := "(metadata.name == 'source-controller' && metadata.namespace == '" + DeliverySystemNamespace + "')"
	observed := "has(metadata.generation) && metadata.generation > 0 && has(status.observedGeneration) && status.observedGeneration == metadata.generation"
	rolledOut := "!has(metadata.deletionTimestamp) && (" + observed + ") && has(spec.replicas) && spec.replicas > 0 && " +
		"has(status.updatedReplicas) && status.updatedReplicas == spec.replicas && has(status.replicas) && status.replicas == spec.replicas"
	replicaFailure := "(has(status.conditions) && status.conditions.exists(c, c.type == 'ReplicaFailure' && c.status == 'True'))"
	available := "has(status.availableReplicas) && has(status.readyReplicas) && (" + source +
		" ? status.availableReplicas > 0 && status.readyReplicas > 0 : status.availableReplicas >= spec.replicas && status.readyReplicas >= spec.replicas)"
	current := "(" + rolledOut + ") && (" + available + ") && !" + replicaFailure
	deadline := "(has(status.conditions) && status.conditions.exists(c, c.type == 'Progressing' && c.status == 'False' && has(c.reason) && c.reason == 'ProgressDeadlineExceeded'))"
	return []any{map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"inProgress": "!(" + observed + ")",
		"failed":     replicaFailure + " || (" + deadline + " && !(" + source + " && (" + current + ")))",
		"current":    current,
	}}
}
