package delivery

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type dynamicSnapshot struct {
	components           []protocol.SystemComponent
	snapshotCounts       map[string]int32
	snapshotsObservation protocol.DeliveryObservation
}
type dynamicInventoryKind struct {
	name, namespace string
	gvr             schema.GroupVersionResource
	project         func(*unstructured.UnstructuredList, time.Time) (protocol.SystemComponent, bool)
}

var dynamicInventoryKinds = []dynamicInventoryKind{
	{"Longhorn volumes", "longhorn-system", schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "volumes"}, func(list *unstructured.UnstructuredList, _ time.Time) (protocol.SystemComponent, bool) {
		return longhornVolumesComponent(list)
	}},
	{"Longhorn nodes", "longhorn-system", schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "nodes"}, func(list *unstructured.UnstructuredList, _ time.Time) (protocol.SystemComponent, bool) {
		return longhornNodesComponent(list)
	}},
	{"Managed certificates", "", schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}, certificatesComponent},
	{"Gateway API gateways", "", schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}, func(list *unstructured.UnstructuredList, _ time.Time) (protocol.SystemComponent, bool) {
		return gatewaysComponent(list)
	}},
	{"Volume snapshots", "", schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}, nil},
}

func (p *ClusterProbe) readDynamicInventory(ctx context.Context, now time.Time) (dynamicSnapshot, time.Duration) {
	result := dynamicSnapshot{snapshotCounts: map[string]int32{}}
	interval := 2 * time.Minute
	certificateExpiries := map[string]time.Time{}
	for _, kind := range dynamicInventoryKinds {
		observed := protocol.DeliveryObservation{State: protocol.ObservationUnavailable}
		var list *unstructured.UnstructuredList
		if p.dynamic != nil {
			var err error
			list, err = p.dynamic.Resource(kind.gvr).Namespace(kind.namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				observed = errorObservation(err)
				if apierrors.IsNotFound(err) {
					observed = protocol.DeliveryObservation{State: protocol.ObservationAbsent, ObservedAt: timePointer(now)}
				}
			} else {
				observed = protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: timePointer(now)}
			}
		}
		if kind.project == nil {
			result.snapshotsObservation = observed
			if list != nil && observed.State == protocol.ObservationCurrent {
				for _, item := range list.Items {
					claim := nestedString(item.Object, "spec", "source", "persistentVolumeClaimName")
					if claim != "" {
						result.snapshotCounts[item.GetNamespace()+"/"+claim]++
					}
				}
			}
			if observed.State != protocol.ObservationCurrent && observed.State != protocol.ObservationAbsent {
				result.components = append(result.components, availabilityComponent(kind.gvr.Resource, kind.name, observed))
			}
			continue
		}
		if list == nil || len(list.Items) == 0 || observed.State != protocol.ObservationCurrent {
			if observed.State == protocol.ObservationCurrent {
				observed.State = protocol.ObservationAbsent
			}
			result.components = append(result.components, availabilityComponent(kind.gvr.Resource, kind.name, observed))
			continue
		}
		projectedAt := time.Now()
		if p.observations != nil {
			projectedAt = p.observations.now()
		}
		component, ok := kind.project(list, projectedAt)
		if ok {
			result.components = append(result.components, withObservation(component, observed))
		}
		// A certificate expiring before the usual refresh must not remain healthy
		// until that refresh. Its public notAfter limits this projection's lifetime.
		if kind.gvr.Resource == "certificates" {
			for _, item := range list.Items {
				expiry, err := time.Parse(time.RFC3339, nestedString(item.Object, "status", "notAfter"))
				if err == nil {
					certificateExpiries[item.GetNamespace()+"/"+item.GetName()] = expiry
				}
				if err == nil && expiry.After(projectedAt) && expiry.Sub(now) < interval {
					interval = expiry.Sub(now)
				}
			}
		}
	}
	// Later dynamic requests can also cross a certificate deadline. Re-evaluate
	// compact public expiry facts once all bounded I/O completes, without changing
	// source timestamps or retaining raw certificate objects in the cache.
	completedAt := time.Now()
	if p.observations != nil {
		completedAt = p.observations.now()
	}
	for i := range result.components {
		component := &result.components[i]
		if component.Kind != "CertificateInventory" {
			continue
		}
		for j := range component.Resources {
			resource := &component.Resources[j]
			expiry, known := certificateExpiries[resource.Namespace+"/"+resource.Name]
			if known && !expiry.After(completedAt) && resource.Health == "healthy" {
				resource.Health = "unavailable"
				resource.Detail += " Expired during inventory refresh."
				component.ReadyReplicas--
			}
		}
		component.Health = replicaHealth(component.DesiredReplicas, component.ReadyReplicas)
		component.Detail = fmt.Sprintf("%d cert-manager Certificates observed; %d Ready.", component.DesiredReplicas, component.ReadyReplicas)
	}
	// Cache only bounded public projections, never full dynamic object bodies.
	result.components = boundSystemComponents(result.components)
	for index := range result.components {
		result.components[index] = copyComponent(result.components[index])
	}
	return result, interval
}
func timePointer(at time.Time) *time.Time { return &at }
