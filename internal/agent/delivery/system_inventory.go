package delivery

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type componentIdentity struct {
	category string
	owner    string
	method   string
	detail   string
}

// inspectSystemComponents is deliberately best-effort. Delivery readiness
// must not flap because a restricted agent cannot list an optional platform
// namespace. A platform-scoped agent reports everything it can observe and the
// inventory timestamp independently communicates freshness.
func (p *ClusterProbe) inspectSystemComponents(ctx context.Context, kubernetesVersion string) []protocol.SystemComponent {
	if p == nil || p.client == nil || !p.platformScope {
		return nil
	}
	components := make([]protocol.SystemComponent, 0, 32)
	var classes []storagev1.StorageClass
	var volumes []protocol.SystemVolumeObservation

	if deployments, err := p.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		for index := range deployments.Items {
			item := &deployments.Items[index]
			if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
				components = append(components, deploymentComponent(item, identity))
			}
		}
	}
	if statefulSets, err := p.client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for index := range statefulSets.Items {
			item := &statefulSets.Items[index]
			if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
				components = append(components, statefulSetComponent(item, identity))
			}
		}
	}
	if daemonSets, err := p.client.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err == nil {
		for index := range daemonSets.Items {
			item := &daemonSets.Items[index]
			if identity, ok := classifySystemWorkload(item.Namespace, item.Name); ok {
				components = append(components, daemonSetComponent(item, identity))
			}
		}
	}
	if pods, err := p.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{LabelSelector: "cnpg.io/cluster"}); err == nil {
		components = append(components, cnpgComponents(pods.Items)...)
	}
	if classList, err := p.client.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{}); err == nil {
		for index := range classList.Items {
			if component, ok := storageClassComponent(&classList.Items[index]); ok {
				components = append(components, component)
			}
		}
		classes = classList.Items
	}
	if claims, err := p.client.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{}); err == nil && len(claims.Items) > 0 {
		component, observed := persistentVolumeComponent(claims.Items, classes, p.volumeSnapshotCounts(ctx))
		components = append(components, component)
		volumes = observed
	}
	if strings.Contains(strings.ToLower(kubernetesVersion), "k3s") {
		components = append(components, protocol.SystemComponent{
			ID: "cluster/distribution/k3s", Name: "K3s", Category: "kubernetes",
			Owner: "cluster", ManagementMethod: "distribution", Kind: "KubernetesDistribution",
			Version: kubernetesVersion, Health: "healthy",
			Detail: "Kubernetes distribution reported by the API server.",
		})
	}
	if containsInventoryString(pDiscoveryAPIs(p), "gateway.networking.k8s.io/v1") {
		components = append(components, protocol.SystemComponent{
			ID: "cluster/api/gateway-api", Name: "Gateway API", Category: "gateway",
			Owner: "cluster", ManagementMethod: "api-extension", Kind: "APIGroup",
			Version: "v1", Health: "healthy", Detail: "Gateway API v1 is served by the cluster.",
		})
	}
	operatorComponents := p.inspectOperatorComponents(ctx)
	for index := range operatorComponents {
		if operatorComponents[index].ManagementMethod != "longhorn" {
			continue
		}
		for _, volume := range volumes {
			if strings.Contains(strings.ToLower(volume.StorageDriver), "longhorn") {
				operatorComponents[index].Volumes = append(operatorComponents[index].Volumes, volume)
			}
		}
	}
	components = append(components, operatorComponents...)
	for index := range components {
		if components[index].Compatibility == "" {
			components[index].Compatibility = "unknown"
		}
		if components[index].UpdateState == "" {
			components[index].UpdateState = "unknown"
		}
	}

	sort.Slice(components, func(i, j int) bool {
		if components[i].Category == components[j].Category {
			return components[i].ID < components[j].ID
		}
		return components[i].Category < components[j].Category
	})
	if len(components) > protocol.MaxDeliveryInventoryEntries {
		components = components[:protocol.MaxDeliveryInventoryEntries]
	}
	return boundSystemComponents(components)
}

func pDiscoveryAPIs(p *ClusterProbe) []string {
	if p == nil || p.discovery == nil {
		return nil
	}
	groups, err := p.discovery.ServerGroups()
	if err != nil || groups == nil {
		return nil
	}
	result := make([]string, 0, len(groups.Groups))
	for _, group := range groups.Groups {
		result = append(result, group.PreferredVersion.GroupVersion)
	}
	return result
}

func classifySystemWorkload(namespace, name string) (componentIdentity, bool) {
	value := strings.ToLower(namespace + "/" + name)
	switch {
	case namespace == DeliverySystemNamespace && strings.HasSuffix(name, "-controller"):
		return componentIdentity{"delivery", "flux", "flux-distribution", "Flux controller installed and upgraded by Astronomer."}, true
	case strings.Contains(value, "astronomer-agent"):
		return componentIdentity{"agent", "astronomer", "helm", "Outbound Astronomer cluster agent."}, true
	case namespace == "astronomer" && containsAny(value, "astronomer-server", "astronomer-frontend", "astronomer-worker", "astronomer-dex"):
		return componentIdentity{"control-plane", "astronomer", "helm", "Astronomer management-plane component."}, true
	case namespace == "astronomer" && containsAny(value, "valkey", "redis"):
		return componentIdentity{"cache", "astronomer", "upstream-helm", "Valkey-compatible cache used by Astronomer."}, true
	case namespace == "longhorn-system":
		return componentIdentity{"storage", "cluster", "helm", "Longhorn distributed storage component; lifecycle remains cluster-owned."}, true
	case namespace == "cert-manager":
		return componentIdentity{"certificates", "cluster", "helm", "cert-manager component; lifecycle remains cluster-owned."}, true
	case containsAny(value, "external-dns"):
		return componentIdentity{"dns", "external", "helm", "External DNS integration; credentials are never inventoried."}, true
	case containsAny(value, "hcloud-cloud-controller", "cloud-controller-manager"):
		return componentIdentity{"load-balancer", "external", "distribution", "Cloud controller and load-balancer integration."}, true
	case containsAny(value, "hcloud-csi", "csi-controller", "csi-node"):
		return componentIdentity{"storage", "cluster", "distribution", "CSI integration supplied by the cluster or infrastructure provider."}, true
	case namespace == "kube-system" && containsAny(value, "traefik", "gateway", "nginx-ingress"):
		return componentIdentity{"gateway", "cluster", "distribution", "Ingress or Gateway implementation supplied by the cluster."}, true
	case namespace == "kube-system" && containsAny(value, "flannel", "cilium", "calico"):
		return componentIdentity{"network", "cluster", "distribution", "Cluster network implementation."}, true
	case namespace == "kube-system" && containsAny(value, "coredns"):
		return componentIdentity{"dns", "cluster", "distribution", "Cluster DNS implementation."}, true
	case namespace == "kube-system" && containsAny(value, "metrics-server"):
		return componentIdentity{"observability", "cluster", "distribution", "Kubernetes resource metrics API."}, true
	case namespace == "kube-system" && containsAny(value, "local-path-provisioner"):
		return componentIdentity{"storage", "cluster", "distribution", "K3s local-path storage provisioner."}, true
	default:
		return componentIdentity{}, false
	}
}

func deploymentComponent(item *appsv1.Deployment, identity componentIdentity) protocol.SystemComponent {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	return workloadComponent(item.Namespace, "Deployment", item.Name, desired, item.Status.ReadyReplicas, item.Spec.Template.Spec.Containers, item.CreationTimestamp.Time, identity)
}

func statefulSetComponent(item *appsv1.StatefulSet, identity componentIdentity) protocol.SystemComponent {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	return workloadComponent(item.Namespace, "StatefulSet", item.Name, desired, item.Status.ReadyReplicas, item.Spec.Template.Spec.Containers, item.CreationTimestamp.Time, identity)
}

func daemonSetComponent(item *appsv1.DaemonSet, identity componentIdentity) protocol.SystemComponent {
	return workloadComponent(item.Namespace, "DaemonSet", item.Name, item.Status.DesiredNumberScheduled, item.Status.NumberReady, item.Spec.Template.Spec.Containers, item.CreationTimestamp.Time, identity)
}

func workloadComponent(namespace, kind, name string, desired, ready int32, containers []corev1.Container, createdAt time.Time, identity componentIdentity) protocol.SystemComponent {
	images := make([]string, 0, len(containers))
	requestsCPU, requestsMemory := apiresource.Quantity{}, apiresource.Quantity{}
	limitsCPU, limitsMemory := apiresource.Quantity{}, apiresource.Quantity{}
	for _, container := range containers {
		if container.Image != "" {
			images = append(images, container.Image)
		}
		addQuantity(&requestsCPU, container.Resources.Requests[corev1.ResourceCPU], desired)
		addQuantity(&requestsMemory, container.Resources.Requests[corev1.ResourceMemory], desired)
		addQuantity(&limitsCPU, container.Resources.Limits[corev1.ResourceCPU], desired)
		addQuantity(&limitsMemory, container.Resources.Limits[corev1.ResourceMemory], desired)
	}
	sort.Strings(images)
	return protocol.SystemComponent{
		ID: fmt.Sprintf("%s/%s/%s", namespace, strings.ToLower(kind), name), Name: name,
		Category: identity.category, Owner: identity.owner, ManagementMethod: identity.method,
		Namespace: namespace, Kind: kind, Version: imageVersion(images), Health: replicaHealth(desired, ready),
		Compatibility: "compatible", UpdateState: "unknown", CreatedAt: timestampString(createdAt),
		DesiredReplicas: desired, ReadyReplicas: ready, HighAvailability: desired > 1,
		Images: images, CPURequest: quantityString(requestsCPU), MemoryRequest: quantityString(requestsMemory),
		CPULimit: quantityString(limitsCPU), MemoryLimit: quantityString(limitsMemory),
		SupportedActions: supportedActions(identity.owner), Detail: identity.detail,
	}
}

func cnpgComponents(pods []corev1.Pod) []protocol.SystemComponent {
	type group struct {
		namespace string
		name      string
		ready     int32
		total     int32
		images    map[string]struct{}
		createdAt time.Time
	}
	groups := map[string]*group{}
	for index := range pods {
		pod := &pods[index]
		name := pod.Labels["cnpg.io/cluster"]
		if name == "" {
			continue
		}
		key := pod.Namespace + "/" + name
		entry := groups[key]
		if entry == nil {
			entry = &group{namespace: pod.Namespace, name: name, images: map[string]struct{}{}}
			groups[key] = entry
		}
		entry.total++
		if entry.createdAt.IsZero() || pod.CreationTimestamp.Time.Before(entry.createdAt) {
			entry.createdAt = pod.CreationTimestamp.Time
		}
		if podReady(pod) {
			entry.ready++
		}
		for _, container := range pod.Spec.Containers {
			if container.Image != "" {
				entry.images[container.Image] = struct{}{}
			}
		}
	}
	result := make([]protocol.SystemComponent, 0, len(groups))
	for _, entry := range groups {
		images := make([]string, 0, len(entry.images))
		for image := range entry.images {
			images = append(images, image)
		}
		sort.Strings(images)
		result = append(result, protocol.SystemComponent{
			ID: entry.namespace + "/cloudnativepg/" + entry.name, Name: entry.name,
			Category: "database", Owner: "astronomer", ManagementMethod: "cloudnative-pg",
			Namespace: entry.namespace, Kind: "Cluster", Version: imageVersion(images),
			Health: replicaHealth(entry.total, entry.ready), DesiredReplicas: entry.total,
			Compatibility: "compatible", UpdateState: "unknown", CreatedAt: timestampString(entry.createdAt),
			ReadyReplicas: entry.ready, HighAvailability: entry.total > 1, Images: images,
			SupportedActions: []string{"view"}, Detail: "PostgreSQL cluster managed by the upstream CloudNativePG operator.",
		})
	}
	return result
}

func storageClassComponent(class *storagev1.StorageClass) (protocol.SystemComponent, bool) {
	if class == nil {
		return protocol.SystemComponent{}, false
	}
	isDefault := class.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" || class.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
	isLonghorn := strings.Contains(strings.ToLower(class.Provisioner), "longhorn") || strings.Contains(strings.ToLower(class.Name), "longhorn")
	if !isDefault && !isLonghorn {
		return protocol.SystemComponent{}, false
	}
	method := "distribution"
	detail := "Cluster storage class."
	if isLonghorn {
		method = "longhorn"
		detail = "Longhorn storage class; volume replica health is managed by Longhorn."
	}
	return protocol.SystemComponent{
		ID: "cluster/storageclass/" + class.Name, Name: class.Name, Category: "storage",
		Owner: "cluster", ManagementMethod: method, Kind: "StorageClass", Health: "healthy",
		Compatibility: "compatible", UpdateState: "unknown", CreatedAt: timestampString(class.CreationTimestamp.Time),
		StorageClass: class.Name, StorageDriver: class.Provisioner, DefaultStorage: isDefault,
		Detail: detail,
	}, true
}

func timestampString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func persistentVolumeComponent(
	claims []corev1.PersistentVolumeClaim,
	classes []storagev1.StorageClass,
	snapshotCounts map[string]int32,
) (protocol.SystemComponent, []protocol.SystemVolumeObservation) {
	type classInfo struct {
		driver    string
		expansion bool
	}
	classByName := make(map[string]classInfo, len(classes))
	for index := range classes {
		class := &classes[index]
		classByName[class.Name] = classInfo{
			driver:    class.Provisioner,
			expansion: class.AllowVolumeExpansion != nil && *class.AllowVolumeExpansion,
		}
	}

	observed := make([]protocol.SystemVolumeObservation, 0, len(claims))
	var bound int32
	var requestedBytes, capacityBytes int64
	drivers := map[string]struct{}{}
	for index := range claims {
		claim := &claims[index]
		className := ""
		if claim.Spec.StorageClassName != nil {
			className = *claim.Spec.StorageClassName
		}
		info := classByName[className]
		requestedQuantity := claim.Spec.Resources.Requests[corev1.ResourceStorage]
		capacityQuantity := claim.Status.Capacity[corev1.ResourceStorage]
		requested := requestedQuantity.Value()
		capacity := capacityQuantity.Value()
		requestedBytes += requested
		capacityBytes += capacity
		if info.driver != "" {
			drivers[info.driver] = struct{}{}
		}
		if claim.Status.Phase == corev1.ClaimBound {
			bound++
		}
		accessModes := make([]string, 0, len(claim.Status.AccessModes))
		for _, mode := range claim.Status.AccessModes {
			accessModes = append(accessModes, string(mode))
		}
		if len(accessModes) == 0 {
			for _, mode := range claim.Spec.AccessModes {
				accessModes = append(accessModes, string(mode))
			}
		}
		volumeMode := ""
		if claim.Spec.VolumeMode != nil {
			volumeMode = string(*claim.Spec.VolumeMode)
		}
		createdAt := ""
		if !claim.CreationTimestamp.IsZero() {
			createdAt = claim.CreationTimestamp.UTC().Format(time.RFC3339)
		}
		observed = append(observed, protocol.SystemVolumeObservation{
			Name: claim.Name, Namespace: claim.Namespace, Phase: string(claim.Status.Phase),
			StorageClass: className, StorageDriver: info.driver,
			RequestedBytes: requested, CapacityBytes: capacity,
			VolumeName: claim.Spec.VolumeName, VolumeMode: volumeMode,
			AccessModes: accessModes, ExpansionAllowed: info.expansion,
			SnapshotCount: snapshotCounts[claim.Namespace+"/"+claim.Name],
			CreatedAt:     createdAt,
		})
	}
	sort.Slice(observed, func(i, j int) bool {
		if observed[i].Namespace == observed[j].Namespace {
			return observed[i].Name < observed[j].Name
		}
		return observed[i].Namespace < observed[j].Namespace
	})
	if len(observed) > 128 {
		observed = observed[:128]
	}
	driver := ""
	if len(drivers) == 1 {
		for value := range drivers {
			driver = value
		}
	} else if len(drivers) > 1 {
		driver = "multiple"
	}
	total := int32(len(claims))
	return protocol.SystemComponent{
		ID: "cluster/core/persistentvolumeclaims", Name: "Persistent volume claims",
		Category: "storage", Owner: "cluster", ManagementMethod: "csi",
		Kind: "PersistentVolumeClaimInventory", Health: replicaHealth(total, bound),
		DesiredReplicas: total, ReadyReplicas: bound, StorageDriver: driver,
		StorageProvisionedBytes: requestedBytes, StorageUsedBytes: capacityBytes,
		Volumes: observed,
		Detail:  fmt.Sprintf("%d persistent volume claims observed; %d Bound. Capacity is Kubernetes-reported claim capacity, not filesystem utilization.", total, bound),
	}, observed
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func replicaHealth(desired, ready int32) string {
	if desired > 0 && ready >= desired {
		return "healthy"
	}
	if ready > 0 {
		return "degraded"
	}
	return "unavailable"
}

func addQuantity(total *apiresource.Quantity, value apiresource.Quantity, replicas int32) {
	if value.IsZero() || replicas < 1 {
		return
	}
	value.Mul(int64(replicas))
	total.Add(value)
}

func quantityString(value apiresource.Quantity) string {
	if value.IsZero() {
		return ""
	}
	return value.String()
}

func imageVersion(images []string) string {
	if len(images) == 0 {
		return ""
	}
	image := images[0]
	if at := strings.LastIndex(image, "@"); at >= 0 {
		return image[at+1:]
	}
	slash := strings.LastIndex(image, "/")
	if colon := strings.LastIndex(image, ":"); colon > slash {
		return image[colon+1:]
	}
	return "latest"
}

func supportedActions(owner string) []string {
	if owner == "astronomer" {
		return []string{"view", "logs", "events"}
	}
	return nil
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsInventoryString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
