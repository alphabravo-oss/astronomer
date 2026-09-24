package delivery

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func (p *ClusterProbe) inspectOperatorComponents(ctx context.Context) []protocol.SystemComponent {
	if p == nil || p.dynamic == nil || !p.platformScope {
		return nil
	}
	result := make([]protocol.SystemComponent, 0, 4)
	if component, ok := p.inspectLonghornVolumes(ctx); ok {
		result = append(result, component)
	}
	if component, ok := p.inspectLonghornNodes(ctx); ok {
		result = append(result, component)
	}
	if component, ok := p.inspectCertificates(ctx); ok {
		result = append(result, component)
	}
	if component, ok := p.inspectGateways(ctx); ok {
		result = append(result, component)
	}
	return result
}

func (p *ClusterProbe) inspectLonghornNodes(ctx context.Context) (protocol.SystemComponent, bool) {
	gvr := schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "nodes"}
	items, err := p.dynamic.Resource(gvr).Namespace("longhorn-system").List(ctx, metav1.ListOptions{})
	if err != nil || len(items.Items) == 0 {
		return protocol.SystemComponent{}, false
	}
	var ready int32
	var maximum, scheduled, available int64
	resources := make([]protocol.SystemResourceObservation, 0, len(items.Items))
	for index := range items.Items {
		item := &items.Items[index]
		isReady := inventoryConditionTrue(item, "Ready")
		if isReady {
			ready++
		}
		nodeMaximum, nodeScheduled, nodeAvailable := int64(0), int64(0), int64(0)
		disks, _, _ := unstructured.NestedMap(item.Object, "status", "diskStatus")
		for _, raw := range disks {
			disk, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			nodeMaximum += nestedInt64(disk, "storageMaximum")
			nodeScheduled += nestedInt64(disk, "storageScheduled")
			nodeAvailable += nestedInt64(disk, "storageAvailable")
		}
		maximum += nodeMaximum
		scheduled += nodeScheduled
		available += nodeAvailable
		health := "unavailable"
		if isReady {
			health = "healthy"
		}
		resources = append(resources, protocol.SystemResourceObservation{
			Name: item.GetName(), Namespace: item.GetNamespace(),
			Group: "longhorn.io", Version: "v1beta2", Plural: "nodes", Kind: "Node",
			Health: health,
			Detail: fmt.Sprintf("%d bytes maximum, %d scheduled, and %d currently available.", nodeMaximum, nodeScheduled, nodeAvailable),
		})
	}
	total := int32(len(items.Items))
	return protocol.SystemComponent{
		ID: "longhorn-system/longhorn.io/nodes", Name: "Longhorn storage nodes",
		Category: "storage", Owner: "cluster", ManagementMethod: "longhorn",
		Namespace: "longhorn-system", Kind: "NodeInventory",
		Health: replicaHealth(total, ready), DesiredReplicas: total, ReadyReplicas: ready,
		StorageDriver:           "driver.longhorn.io",
		StorageProvisionedBytes: maximum, StorageUsedBytes: scheduled,
		Resources: resources,
		Detail:    fmt.Sprintf("%d Longhorn nodes observed; %d Ready. %d bytes are currently schedulable.", total, ready, available),
	}, true
}

func (p *ClusterProbe) volumeSnapshotCounts(ctx context.Context) map[string]int32 {
	result := map[string]int32{}
	if p == nil || p.dynamic == nil {
		return result
	}
	gvr := schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
	items, err := p.dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return result
	}
	for index := range items.Items {
		item := &items.Items[index]
		claim := nestedString(item.Object, "spec", "source", "persistentVolumeClaimName")
		if claim != "" {
			result[item.GetNamespace()+"/"+claim]++
		}
	}
	return result
}

func (p *ClusterProbe) inspectLonghornVolumes(ctx context.Context) (protocol.SystemComponent, bool) {
	gvr := schema.GroupVersionResource{Group: "longhorn.io", Version: "v1beta2", Resource: "volumes"}
	items, err := p.dynamic.Resource(gvr).Namespace("longhorn-system").List(ctx, metav1.ListOptions{})
	if err != nil || len(items.Items) == 0 {
		return protocol.SystemComponent{}, false
	}
	var healthy int32
	var provisioned, used int64
	var minimumReplicas int32
	resources := make([]protocol.SystemResourceObservation, 0, len(items.Items))
	for index := range items.Items {
		item := &items.Items[index]
		robustness := nestedString(item.Object, "status", "robustness")
		if strings.EqualFold(robustness, "healthy") {
			healthy++
		}
		provisioned += nestedInt64(item.Object, "spec", "size")
		used += nestedInt64(item.Object, "status", "actualSize")
		replicas := int32(nestedInt64(item.Object, "spec", "numberOfReplicas"))
		if replicas > 0 && (minimumReplicas == 0 || replicas < minimumReplicas) {
			minimumReplicas = replicas
		}
		resources = append(resources, protocol.SystemResourceObservation{
			Name: item.GetName(), Namespace: item.GetNamespace(),
			Group: "longhorn.io", Version: "v1beta2", Plural: "volumes", Kind: "Volume",
			Health: strings.ToLower(robustness),
			Detail: fmt.Sprintf("%d configured replicas; %d bytes actual size.", replicas, nestedInt64(item.Object, "status", "actualSize")),
		})
	}
	total := int32(len(items.Items))
	return protocol.SystemComponent{
		ID: "longhorn-system/longhorn.io/volumes", Name: "Longhorn volumes",
		Category: "storage", Owner: "cluster", ManagementMethod: "longhorn",
		Namespace: "longhorn-system", Kind: "VolumeInventory",
		Health: replicaHealth(total, healthy), DesiredReplicas: total, ReadyReplicas: healthy,
		HighAvailability: minimumReplicas > 1, StorageDriver: "driver.longhorn.io",
		StorageProvisionedBytes: provisioned, StorageUsedBytes: used,
		StorageReplicaCount: minimumReplicas,
		Resources:           resources,
		Detail:              fmt.Sprintf("%d volumes observed; %d healthy. Replica count is the lowest configured value across observed volumes.", total, healthy),
	}, true
}

func (p *ClusterProbe) inspectCertificates(ctx context.Context) (protocol.SystemComponent, bool) {
	gvr := schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
	items, err := p.dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil || len(items.Items) == 0 {
		return protocol.SystemComponent{}, false
	}
	var ready int32
	resources := make([]protocol.SystemResourceObservation, 0, len(items.Items))
	for index := range items.Items {
		item := &items.Items[index]
		detail, expired := certificateInventoryDetail(item, time.Now())
		isReady := inventoryConditionTrue(item, "Ready") && !expired
		if isReady {
			ready++
		}
		health := "unavailable"
		if isReady {
			health = "healthy"
		}
		resources = append(resources, protocol.SystemResourceObservation{
			Name: item.GetName(), Namespace: item.GetNamespace(),
			Group: "cert-manager.io", Version: "v1", Plural: "certificates", Kind: "Certificate",
			Health: health, Detail: detail,
		})
	}
	total := int32(len(items.Items))
	return protocol.SystemComponent{
		ID: "cluster/cert-manager.io/certificates", Name: "Managed certificates",
		Category: "certificates", Owner: "cluster", ManagementMethod: "cert-manager",
		Kind: "CertificateInventory", Health: replicaHealth(total, ready),
		DesiredReplicas: total, ReadyReplicas: ready,
		Resources: resources,
		Detail:    fmt.Sprintf("%d cert-manager Certificates observed; %d Ready.", total, ready),
	}, true
}

func (p *ClusterProbe) inspectGateways(ctx context.Context) (protocol.SystemComponent, bool) {
	gvr := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	items, err := p.dynamic.Resource(gvr).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil || len(items.Items) == 0 {
		return protocol.SystemComponent{}, false
	}
	var ready int32
	var listeners int64
	var attachedRoutes int64
	resources := make([]protocol.SystemResourceObservation, 0, len(items.Items))
	for index := range items.Items {
		item := &items.Items[index]
		programmed := inventoryConditionTrue(item, "Programmed") || inventoryConditionTrue(item, "Ready")
		if programmed {
			ready++
		}
		statuses, _, _ := unstructured.NestedSlice(item.Object, "status", "listeners")
		listeners += int64(len(statuses))
		for _, status := range statuses {
			if object, ok := status.(map[string]any); ok {
				attachedRoutes += nestedInt64(object, "attachedRoutes")
			}
		}
		health := "unavailable"
		if programmed {
			health = "healthy"
		}
		resources = append(resources, protocol.SystemResourceObservation{
			Name: item.GetName(), Namespace: item.GetNamespace(),
			Group: "gateway.networking.k8s.io", Version: "v1", Plural: "gateways", Kind: "Gateway",
			Health: health, Detail: fmt.Sprintf("%d listener status entries.", len(statuses)),
		})
	}
	total := int32(len(items.Items))
	return protocol.SystemComponent{
		ID: "cluster/gateway.networking.k8s.io/gateways", Name: "Gateway API gateways",
		Category: "gateway", Owner: "cluster", ManagementMethod: "gateway-api",
		Kind: "GatewayInventory", Health: replicaHealth(total, ready),
		DesiredReplicas: total, ReadyReplicas: ready,
		Resources: resources,
		Detail:    fmt.Sprintf("%d Gateways observed; %d programmed, %d listeners, and %d attached routes.", total, ready, listeners, attachedRoutes),
	}, true
}

func inventoryConditionTrue(item *unstructured.Unstructured, wanted string) bool {
	conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if nestedString(condition, "type") == wanted && strings.EqualFold(nestedString(condition, "status"), "true") {
			return true
		}
	}
	return false
}

func nestedString(object map[string]any, fields ...string) string {
	value, _, _ := unstructured.NestedString(object, fields...)
	return value
}

func nestedInt64(object map[string]any, fields ...string) int64 {
	value, found, _ := unstructured.NestedFieldNoCopy(object, fields...)
	if !found {
		return 0
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}
