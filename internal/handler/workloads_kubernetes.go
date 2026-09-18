package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
)

func (h *WorkloadHandler) listWorkloads(ctx context.Context, clusterID, namespace, kind string) ([]map[string]any, error) {
	kinds := []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}
	if kind != "" {
		kinds = []string{kind}
	}
	var items []map[string]any
	for _, k := range kinds {
		listPath, err := workloadListPath(k, namespace)
		if err != nil {
			continue
		}
		continueToken := ""
		previousToken := ""
		for {
			query := url.Values{}
			query.Set("limit", fmt.Sprintf("%d", upstreamKubernetesPageLimit))
			if continueToken != "" {
				query.Set("continue", continueToken)
			}
			var wl workloadList
			if err := h.getJSON(ctx, clusterID, listPath+"?"+query.Encode(), &wl); err != nil {
				return nil, err
			}
			for _, item := range wl.Items {
				// Kubernetes List responses only stamp `kind` on the
				// outer wrapper (e.g. "DeploymentList"); each item in
				// `.items` arrives with kind="". Stamp it from the
				// loop kind so workloadToMap + the frontend
				// filter-by-kind both see the correct value.
				if item.Kind == "" {
					item.Kind = k
				}
				items = append(items, workloadToMap(clusterID, item))
			}
			continueToken = wl.Metadata.Continue
			if continueToken == "" {
				break
			}
			if continueToken == previousToken {
				return nil, fmt.Errorf("workload pagination continuation did not advance")
			}
			previousToken = continueToken
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i]["namespace"].(string) == items[j]["namespace"].(string) {
			return items[i]["name"].(string) < items[j]["name"].(string)
		}
		return items[i]["namespace"].(string) < items[j]["namespace"].(string)
	})
	return items, nil
}

func (h *WorkloadHandler) getWorkload(ctx context.Context, clusterID, kind, namespace, name string) (map[string]any, error) {
	resource, err := h.fetchWorkloadResource(ctx, clusterID, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	return workloadToMap(clusterID, resource), nil
}

func (h *WorkloadHandler) fetchWorkloadResource(ctx context.Context, clusterID, kind, namespace, name string) (workloadResource, error) {
	path, err := workloadPath(kind, namespace, name)
	if err != nil {
		return workloadResource{}, err
	}
	var resource workloadResource
	err = h.getJSON(ctx, clusterID, path, &resource)
	return resource, err
}

func (h *WorkloadHandler) listPods(ctx context.Context, clusterID, namespace, selector string) ([]map[string]any, error) {
	path := "/api/v1/pods"
	if namespace != "" {
		path = fmt.Sprintf("/api/v1/namespaces/%s/pods", namespace)
	}
	query := url.Values{}
	query.Set("limit", fmt.Sprintf("%d", upstreamKubernetesPageLimit))
	if selector != "" {
		query.Set("labelSelector", selector)
	}
	items := make([]map[string]any, 0)
	continueToken := ""
	previousToken := ""
	for {
		if continueToken == "" {
			query.Del("continue")
		} else {
			query.Set("continue", continueToken)
		}
		var pods podList
		if err := h.getJSON(ctx, clusterID, path+"?"+query.Encode(), &pods); err != nil {
			return nil, err
		}
		for _, pod := range pods.Items {
			items = append(items, podToMap(clusterID, pod))
		}
		continueToken = pods.Metadata.Continue
		if continueToken == "" {
			break
		}
		if continueToken == previousToken {
			return nil, fmt.Errorf("pod pagination continuation did not advance")
		}
		previousToken = continueToken
	}
	return items, nil
}

const upstreamKubernetesPageLimit = 500

// listPodsPage follows Kubernetes continuation tokens while retaining only the
// requested offset window. Unlike listPods, no single tunnel response can
// contain an unbounded PodList. Kubernetes supplies remainingItemCount for
// unfiltered lists, which preserves an exact UI row count without transferring
// every pod body through the management plane.
func (h *WorkloadHandler) listPodsPage(ctx context.Context, clusterID, namespace string, limit, offset int) ([]map[string]any, *int64, bool, error) {
	path := "/api/v1/pods"
	if namespace != "" {
		path = fmt.Sprintf("/api/v1/namespaces/%s/pods", namespace)
	}
	remainingSkip := max(offset, 0)
	remainingTake := max(limit, 1)
	items := make([]map[string]any, 0, remainingTake)
	continueToken := ""
	previousToken := ""
	var total *int64

	for remainingTake > 0 {
		requestLimit := min(upstreamKubernetesPageLimit, remainingSkip+remainingTake)
		query := url.Values{}
		query.Set("limit", fmt.Sprintf("%d", requestLimit))
		if continueToken != "" {
			query.Set("continue", continueToken)
		}

		var pods podList
		if err := h.getJSON(ctx, clusterID, path+"?"+query.Encode(), &pods); err != nil {
			return nil, nil, false, err
		}
		if total == nil && pods.Metadata.RemainingItemCount != nil {
			count := int64(len(pods.Items)) + *pods.Metadata.RemainingItemCount
			total = &count
		}
		for _, pod := range pods.Items {
			if remainingSkip > 0 {
				remainingSkip--
				continue
			}
			if remainingTake == 0 {
				break
			}
			items = append(items, podToMap(clusterID, pod))
			remainingTake--
		}

		continueToken = pods.Metadata.Continue
		if continueToken == "" {
			break
		}
		if continueToken == previousToken {
			return nil, nil, false, fmt.Errorf("pod pagination continuation did not advance")
		}
		previousToken = continueToken
	}

	hasMore := continueToken != ""
	if total != nil {
		hasMore = int64(offset+len(items)) < *total
	}
	return items, total, hasMore, nil
}

func (h *WorkloadHandler) countPods(ctx context.Context, clusterID, namespace string) (int64, error) {
	path := "/api/v1/pods"
	if namespace != "" {
		path = fmt.Sprintf("/api/v1/namespaces/%s/pods", namespace)
	}
	var pods podList
	if err := h.getJSON(ctx, clusterID, path+"?limit=1", &pods); err != nil {
		return 0, err
	}
	count := int64(len(pods.Items))
	if pods.Metadata.RemainingItemCount != nil {
		count += *pods.Metadata.RemainingItemCount
		return count, nil
	}
	if pods.Metadata.Continue != "" {
		return 0, fmt.Errorf("apiserver omitted pod remainingItemCount")
	}
	return count, nil
}

func (h *WorkloadHandler) listPodsAcrossNamespacesPage(ctx context.Context, clusterID string, namespaces map[string]struct{}, limit, offset int) ([]map[string]any, int64, bool, error) {
	names := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		names = append(names, namespace)
	}
	sort.Strings(names)

	remainingOffset := int64(max(offset, 0))
	remainingLimit := max(limit, 1)
	items := make([]map[string]any, 0, remainingLimit)
	var total int64
	for _, namespace := range names {
		count, err := h.countPods(ctx, clusterID, namespace)
		if err != nil {
			return nil, 0, false, err
		}
		total += count
		if remainingLimit == 0 {
			continue
		}
		if remainingOffset >= count {
			remainingOffset -= count
			continue
		}
		page, _, _, err := h.listPodsPage(ctx, clusterID, namespace, remainingLimit, int(remainingOffset))
		if err != nil {
			return nil, 0, false, err
		}
		items = append(items, page...)
		remainingLimit -= len(page)
		remainingOffset = 0
	}
	return items, total, int64(offset+len(items)) < total, nil
}

// listPodsFieldSelector lists pods filtered server-side by a fieldSelector
// (e.g. spec.nodeName=<node>) so the whole-cluster pod set never crosses the
// tunnel just to be filtered in Go.
func (h *WorkloadHandler) listPodsFieldSelector(ctx context.Context, clusterID, fieldSelector string) ([]map[string]any, error) {
	path := "/api/v1/pods?fieldSelector=" + url.QueryEscape(fieldSelector)
	var pods podList
	if err := h.getJSON(ctx, clusterID, path, &pods); err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(pods.Items))
	for _, pod := range pods.Items {
		items = append(items, podToMap(clusterID, pod))
	}
	return items, nil
}

// listPodMetadata fetches a lightweight PartialObjectMetadataList of pods. The
// content-negotiation Accept header asks the apiserver to strip spec/status;
// it falls back to a normal PodList when partial metadata isn't available, and
// podMetadataList decodes either shape.
func (h *WorkloadHandler) listPodMetadata(ctx context.Context, clusterID, path string) (*podMetadataList, error) {
	if h.requester == nil {
		return nil, fmt.Errorf("tunnel requester not configured")
	}
	headers := requestHeaders("")
	headers["Accept"] = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1,application/json"
	var out podMetadataList
	continueToken := ""
	previousToken := ""
	for {
		query := url.Values{}
		query.Set("limit", fmt.Sprintf("%d", upstreamKubernetesPageLimit))
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path+"?"+query.Encode(), nil, headers)
		if err != nil {
			return nil, err
		}
		if err := ensureSuccess(resp); err != nil {
			return nil, err
		}
		var page podMetadataList
		if err := parseJSONResponse(resp, &page); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, page.Items...)
		continueToken = page.Metadata.Continue
		if continueToken == "" {
			return &out, nil
		}
		if continueToken == previousToken {
			return nil, fmt.Errorf("pod metadata pagination continuation did not advance")
		}
		previousToken = continueToken
	}
}

func (h *WorkloadHandler) listNamespaces(ctx context.Context, clusterID string) (*namespaceList, error) {
	var out namespaceList
	continueToken := ""
	previousToken := ""
	for {
		query := url.Values{}
		query.Set("limit", fmt.Sprintf("%d", upstreamKubernetesPageLimit))
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		var page namespaceList
		if err := h.getJSON(ctx, clusterID, "/api/v1/namespaces?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, page.Items...)
		continueToken = page.Metadata.Continue
		if continueToken == "" {
			return &out, nil
		}
		if continueToken == previousToken {
			return nil, fmt.Errorf("namespace pagination continuation did not advance")
		}
		previousToken = continueToken
	}
}

func (h *WorkloadHandler) getNodes(ctx context.Context, clusterID string) ([]map[string]any, error) {
	var nodes nodeList
	continueToken := ""
	previousToken := ""
	for {
		query := url.Values{}
		query.Set("limit", fmt.Sprintf("%d", upstreamKubernetesPageLimit))
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		var page nodeList
		if err := h.getJSON(ctx, clusterID, "/api/v1/nodes?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		nodes.Items = append(nodes.Items, page.Items...)
		continueToken = page.Metadata.Continue
		if continueToken == "" {
			break
		}
		if continueToken == previousToken {
			return nil, fmt.Errorf("node pagination continuation did not advance")
		}
		previousToken = continueToken
	}
	pods, _ := h.listPods(ctx, clusterID, "", "")
	podCounts := map[string]int{}
	for _, pod := range pods {
		node, _ := pod["node"].(string)
		podCounts[node]++
	}
	items := make([]map[string]any, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		items = append(items, nodeSummaryMap(node, podCounts[node.Metadata.Name]))
	}
	if h.metrics != nil {
		isLocal := clusterID == h.localClusterID && h.localClusterID != ""
		layerNodeSummaryUsage(items, h.metrics.Get(ctx, clusterID, isLocal))
	}
	return items, nil
}

func layerNodeSummaryUsage(items []map[string]any, snapshot clustermetrics.Snapshot) {
	for _, item := range items {
		name, _ := item["name"].(string)
		metrics, ok := snapshot.Nodes[name]
		if !ok {
			continue
		}
		item["cpuUsage"] = metrics.CPUUsageMillicores
		item["memoryUsage"] = metrics.MemoryUsageBytes
	}
}

func (h *WorkloadHandler) getNodeDetail(ctx context.Context, clusterID, nodeName string) (map[string]any, error) {
	var node struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Taints        []map[string]any `json:"taints"`
			Unschedulable bool             `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			NodeInfo map[string]any    `json:"nodeInfo"`
			Capacity map[string]string `json:"capacity"`
			Images   []struct {
				Names     []string `json:"names"`
				SizeBytes int64    `json:"sizeBytes"`
			} `json:"images"`
			Addresses  []map[string]string `json:"addresses"`
			Conditions []map[string]string `json:"conditions"`
		} `json:"status"`
	}
	if err := h.getJSON(ctx, clusterID, "/api/v1/nodes/"+nodeName, &node); err != nil {
		return nil, err
	}
	// Scope the pod list to this node server-side (mirrors DrainNode) instead of
	// pulling every pod in the cluster and filtering in Go.
	pods, _ := h.listPodsFieldSelector(ctx, clusterID, "spec.nodeName="+nodeName)
	nodePods := make([]map[string]any, 0, len(pods))
	nodePodKeys := make([]string, 0, len(pods))
	for _, pod := range pods {
		ns, _ := pod["namespace"].(string)
		pn, _ := pod["name"].(string)
		nodePodKeys = append(nodePodKeys, ns+"/"+pn)
		nodePods = append(nodePods, map[string]any{
			"name":      pod["name"],
			"namespace": pod["namespace"],
			"status":    pod["status"],
			"ready":     pod["ready"],
			"restarts":  pod["restarts"],
			"createdAt": pod["createdAt"],
			"images":    pod["images"],
			"metadata": map[string]any{
				"name":      pod["name"],
				"namespace": pod["namespace"],
			},
		})
	}

	// Pull real CPU/memory usage from the metrics provider. The provider
	// already fetches metrics-server data for all nodes/pods of the cluster
	// in a single round trip, so the per-node lookup is a cache hit.
	var nodeUsageCPU, nodeUsageMem int64
	if h.metrics != nil {
		isLocal := clusterID == h.localClusterID && h.localClusterID != ""
		nm := h.metrics.GetNode(ctx, clusterID, nodeName, isLocal)
		nodeUsageCPU = nm.CPUUsageMillicores
		nodeUsageMem = nm.MemoryUsageBytes
		if pmList := h.metrics.PodsByNode(ctx, clusterID, nodeName, nodePodKeys, isLocal); len(pmList) > 0 {
			byKey := make(map[string]clustermetrics.PodMetrics, len(pmList))
			for _, pm := range pmList {
				byKey[pm.Namespace+"/"+pm.Name] = pm
			}
			for i, p := range nodePods {
				ns, _ := p["namespace"].(string)
				pn, _ := p["name"].(string)
				if pm, ok := byKey[ns+"/"+pn]; ok {
					nodePods[i]["cpuUsage"] = pm.CPUUsageMillicores
					nodePods[i]["memoryUsage"] = pm.MemoryUsageBytes
				}
			}
		}
	}

	var events eventList
	_ = h.getJSON(ctx, clusterID, "/api/v1/events?fieldSelector="+url.QueryEscape("involvedObject.name="+nodeName+",involvedObject.kind=Node"), &events)
	nodeEvents := make([]map[string]any, 0, len(events.Items))
	for _, evt := range events.Items {
		nodeEvents = append(nodeEvents, map[string]any{
			"type": evt.Type, "reason": evt.Reason, "message": evt.Message, "count": evt.Count,
			"firstTimestamp": evt.FirstTimestamp, "lastTimestamp": evt.LastTimestamp,
		})
	}
	// Surface kubeletVersion at the top level (cluster-list detail card reads
	// `node.kubeletVersion`) AND keep it in nodeInfo (the node page also reads
	// `node.nodeInfo.kubeletVersion`). Both are commonly used.
	kubeletVersion, _ := node.Status.NodeInfo["kubeletVersion"].(string)
	return map[string]any{
		"name":           node.Metadata.Name,
		"status":         nodeStatus(node.Status.Conditions, node.Spec.Unschedulable),
		"roles":          nodeRoles(node.Metadata.Labels),
		"labels":         defaultMap(node.Metadata.Labels),
		"annotations":    defaultMap(node.Metadata.Annotations),
		"createdAt":      node.Metadata.CreationTimestamp.UTC().Format(time.RFC3339),
		"nodeInfo":       node.Status.NodeInfo,
		"kubeletVersion": kubeletVersion,
		"cpuCapacity":    parseCPU(node.Status.Capacity["cpu"]),
		"cpuUsage":       int(nodeUsageCPU),
		"memoryCapacity": parseMemory(node.Status.Capacity["memory"]),
		"memoryUsage":    int(nodeUsageMem),
		"podCapacity":    parseInt(node.Status.Capacity["pods"]),
		"podCount":       len(nodePods),
		"addresses":      nonNilSlice(node.Status.Addresses),
		"conditions":     nonNilSlice(node.Status.Conditions),
		"taints":         nonNilSlice(node.Spec.Taints),
		"images":         imagesToMaps(node.Status.Images),
		"pods":           nonNilAny(nodePods),
		"events":         nonNilAny(nodeEvents),
		"unschedulable":  node.Spec.Unschedulable,
	}, nil
}

func (h *WorkloadHandler) getJSON(ctx context.Context, clusterID, path string, out any) error {
	if h.requester == nil {
		return fmt.Errorf("tunnel requester not configured")
	}
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return err
	}
	if err := ensureSuccess(resp); err != nil {
		return err
	}
	return parseJSONResponse(resp, out)
}

func workloadListPath(kind, namespace string) (string, error) {
	group, version, plural, err := workloadGVK(kind)
	if err != nil {
		return "", err
	}
	if namespace != "" {
		return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", group, version, namespace, plural), nil
	}
	return fmt.Sprintf("/apis/%s/%s/%s", group, version, plural), nil
}

func workloadPath(kind, namespace, name string) (string, error) {
	group, version, plural, err := workloadGVK(kind)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s/%s", group, version, namespace, plural, name), nil
}

func scalePath(kind, namespace, name string) (string, error) {
	group, version, plural, err := workloadGVK(kind)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s/%s/scale", group, version, namespace, plural, name), nil
}

func workloadGVK(kind string) (group, version, plural string, err error) {
	switch strings.ToLower(kind) {
	case "deployment":
		return "apps", "v1", "deployments", nil
	case "statefulset":
		return "apps", "v1", "statefulsets", nil
	case "daemonset":
		return "apps", "v1", "daemonsets", nil
	case "replicaset":
		return "apps", "v1", "replicasets", nil
	case "job":
		return "batch", "v1", "jobs", nil
	case "cronjob":
		return "batch", "v1", "cronjobs", nil
	default:
		return "", "", "", fmt.Errorf("unsupported workload kind %q", kind)
	}
}

func workloadToMap(clusterID string, item workloadResource) map[string]any {
	desired := int32(1)
	if item.Spec.Replicas != nil {
		desired = *item.Spec.Replicas
	}
	images := make([]string, 0, len(item.Spec.Template.Spec.Containers))
	for _, c := range item.Spec.Template.Spec.Containers {
		images = append(images, c.Image)
	}
	return map[string]any{
		"name":            item.Metadata.Name,
		"namespace":       item.Metadata.Namespace,
		"kind":            item.Kind,
		"clusterId":       clusterID,
		"clusterName":     clusterID,
		"status":          workloadStatus(item),
		"ready":           fmt.Sprintf("%d/%d", item.Status.ReadyReplicas, desired),
		"upToDate":        item.Status.UpdatedReplicas,
		"available":       item.Status.AvailableReplicas,
		"replicas":        item.Status.Replicas,
		"desiredReplicas": desired,
		"images":          images,
		"labels":          defaultMap(item.Metadata.Labels),
		"annotations":     defaultMap(item.Metadata.Annotations),
		"createdAt":       item.Metadata.CreationTimestamp.UTC().Format(time.RFC3339),
		"age":             humanAge(item.Metadata.CreationTimestamp),
	}
}

func workloadStatus(item workloadResource) string {
	switch item.Kind {
	case "Job":
		if item.Status.Succeeded > 0 {
			return "Succeeded"
		}
	case "Deployment", "StatefulSet", "ReplicaSet":
		if item.Status.ReadyReplicas >= valueOrOne(item.Spec.Replicas) && item.Status.ReadyReplicas > 0 {
			return "Running"
		}
	case "DaemonSet":
		if item.Status.ReadyReplicas > 0 {
			return "Running"
		}
	case "CronJob":
		return "Running"
	}
	if item.Status.Replicas > 0 || item.Status.Active > 0 {
		return "Pending"
	}
	return "Unknown"
}

type workloadOperationCreator interface {
	CreateWorkloadOperation(context.Context, sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error)
}
