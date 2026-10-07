package delivery

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func sharedSystemComponents(snapshots map[observation.Kind]observation.Snapshot, discovery discoverySnapshot, dynamic dynamicSnapshot, now time.Time) []protocol.SystemComponent {
	result := []protocol.SystemComponent{}
	for _, kind := range observation.Kinds() {
		snapshot := snapshots[kind]
		result = append(result, workloadSnapshotComponents(snapshot)...)
		if snapshot.Observation.State != protocol.ObservationCurrent {
			result = append(result, availabilityComponent(string(kind), string(kind)+" inventory", snapshot.Observation))
		}
	}
	classes := []storagev1.StorageClass{}
	for _, item := range snapshots[observation.StorageClasses].StorageClasses {
		classes = append(classes, *item)
	}
	claims := []corev1.PersistentVolumeClaim{}
	for _, item := range snapshots[observation.Claims].Claims {
		claims = append(claims, *item)
	}
	var volumes []protocol.SystemVolumeObservation
	if len(claims) > 0 {
		component, observed := persistentVolumeComponent(claims, classes, dynamic.snapshotCounts)
		volumes = observed
		source := combineObservations(snapshots[observation.Claims].Observation, snapshots[observation.StorageClasses].Observation)
		// Snapshot capability absence is not a failed PVC observation; denied or
		// unavailable snapshot reads cannot masquerade as verified snapshot counts.
		if dynamic.snapshotsObservation.State != protocol.ObservationAbsent {
			source = combineObservations(source, dynamic.snapshotsObservation)
		}
		result = append(result, withObservation(component, source))
	}
	for _, component := range dynamic.components {
		component = copyComponent(component)
		if component.ManagementMethod == "longhorn" {
			for _, volume := range volumes {
				if strings.Contains(strings.ToLower(volume.StorageDriver), "longhorn") {
					component.Volumes = append(component.Volumes, volume)
				}
			}
			if len(component.Volumes) > 0 {
				source := combineObservations(*component.Observation, snapshots[observation.Claims].Observation)
				source = combineObservations(source, snapshots[observation.StorageClasses].Observation)
				if dynamic.snapshotsObservation.State != protocol.ObservationAbsent {
					source = combineObservations(source, dynamic.snapshotsObservation)
				}
				component = withObservation(component, source)
			}
		}
		result = append(result, component)
	}
	if strings.Contains(strings.ToLower(discovery.version), "k3s") {
		result = append(result, withObservation(protocol.SystemComponent{ID: "cluster/distribution/k3s", Name: "K3s", Category: "kubernetes", Owner: "cluster", ManagementMethod: "distribution", Kind: "KubernetesDistribution", Version: discovery.version, Health: "healthy", Detail: "Kubernetes distribution reported by the API server."}, discovery.observation))
	}
	if discovery.groupsError != nil {
		result = append(result, availabilityComponent("api-groups", "API group discovery", errorObservation(discovery.groupsError)))
	} else if containsInventoryString(discovery.groups, "gateway.networking.k8s.io/v1") {
		result = append(result, withObservation(protocol.SystemComponent{ID: "cluster/api/gateway-api", Name: "Gateway API", Category: "gateway", Owner: "cluster", ManagementMethod: "api-extension", Kind: "APIGroup", Version: "v1", Health: "healthy", Detail: "Gateway API v1 is served by the cluster."}, discovery.observation))
	}
	for i := range result {
		if result[i].Observation != nil && result[i].Observation.State == protocol.ObservationCurrent && result[i].Observation.ObservedAt != nil && now.Sub(*result[i].Observation.ObservedAt) > 4*time.Minute {
			source := *result[i].Observation
			source.State = protocol.ObservationStale
			result[i] = withObservation(result[i], source)
		}
	}
	return normalizeSystemComponents(result)
}
func workloadSnapshotComponents(snapshot observation.Snapshot) []protocol.SystemComponent {
	result := []protocol.SystemComponent{}
	for _, item := range snapshot.Deployments {
		if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
			result = append(result, withObservation(deploymentComponent(item, identity), snapshot.Observation))
		}
	}
	for _, item := range snapshot.StatefulSets {
		if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
			result = append(result, withObservation(statefulSetComponent(item, identity), snapshot.Observation))
		}
	}
	for _, item := range snapshot.DaemonSets {
		if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
			result = append(result, withObservation(daemonSetComponent(item, identity), snapshot.Observation))
		}
	}
	if len(snapshot.Pods) > 0 {
		pods := make([]corev1.Pod, 0, len(snapshot.Pods))
		for _, item := range snapshot.Pods {
			pods = append(pods, *item)
		}
		for _, component := range cnpgComponents(pods) {
			result = append(result, withObservation(component, snapshot.Observation))
		}
	}
	for _, item := range snapshot.StorageClasses {
		if component, ok := storageClassComponent(item); ok {
			result = append(result, withObservation(component, snapshot.Observation))
		}
	}
	return result
}
func withObservation(component protocol.SystemComponent, source protocol.DeliveryObservation) protocol.SystemComponent {
	component.Observation = &source
	if source.ObservedAt != nil {
		component.Observation.ObservedAt = timePointer(*source.ObservedAt)
	}
	if source.State != protocol.ObservationCurrent {
		component.Health = "unavailable"
		component.SupportedActions = nil
		for i := range component.Resources {
			component.Resources[i].Health = "unavailable"
		}
	}
	return component
}
func availabilityComponent(kind, name string, source protocol.DeliveryObservation) protocol.SystemComponent {
	return withObservation(protocol.SystemComponent{ID: "cluster/observation/" + strings.ToLower(kind), Name: name, Category: "observation", Owner: "cluster", ManagementMethod: "observation", Kind: "InventoryAvailability", Health: "unavailable", Detail: fmt.Sprintf("Observation source is %s; this is not proof that resources are missing.", source.State)}, source)
}
func copyComponent(component protocol.SystemComponent) protocol.SystemComponent {
	if component.Observation != nil {
		source := *component.Observation
		if source.ObservedAt != nil {
			source.ObservedAt = timePointer(*source.ObservedAt)
		}
		component.Observation = &source
	}
	component.Images = cloneInventorySlice(component.Images)
	component.SupportedActions = cloneInventorySlice(component.SupportedActions)
	component.Resources = cloneInventorySlice(component.Resources)
	component.Volumes = cloneInventorySlice(component.Volumes)
	for i := range component.Volumes {
		component.Volumes[i].AccessModes = cloneInventorySlice(component.Volumes[i].AccessModes)
	}
	return component
}

// Allocate exact-sized backing arrays: slicing a huge projection to128 would
// otherwise keep its entire allocation alive in the refresh cache.
func cloneInventorySlice[T any](items []T) []T {
	if items == nil {
		return nil
	}
	out := make([]T, len(items))
	copy(out, items)
	return out
}
