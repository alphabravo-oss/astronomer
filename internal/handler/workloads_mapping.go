package handler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
)

func podToMap(clusterID string, pod podResource) map[string]any {
	readyCount := 0
	restarts := 0
	containers := make([]map[string]any, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		status := "waiting"
		ready := false
		restartCount := 0
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != container.Name {
				continue
			}
			ready = cs.Ready
			restartCount = cs.RestartCount
			if cs.State.Running != nil {
				status = "running"
			} else if cs.State.Terminated != nil {
				status = "terminated"
			}
			if cs.Ready {
				readyCount++
			}
			restarts += cs.RestartCount
		}
		ports := make([]map[string]any, 0, len(container.Ports))
		for _, port := range container.Ports {
			ports = append(ports, map[string]any{"name": port.Name, "containerPort": port.ContainerPort, "protocol": port.Protocol})
		}
		containers = append(containers, map[string]any{
			"name": container.Name, "image": container.Image, "status": status, "ready": ready, "restartCount": restartCount, "ports": ports,
		})
	}
	conditions := make([]map[string]any, 0, len(pod.Status.Conditions))
	for _, cond := range pod.Status.Conditions {
		conditions = append(conditions, map[string]any{
			"type": cond.Type, "status": cond.Status, "reason": cond.Reason, "message": cond.Message, "lastTransition": cond.LastTransitionTime,
		})
	}
	images := make([]string, 0, len(containers))
	for _, c := range containers {
		images = append(images, c["image"].(string))
	}
	return map[string]any{
		"name":       pod.Metadata.Name,
		"namespace":  pod.Metadata.Namespace,
		"clusterId":  clusterID,
		"phase":      pod.Status.Phase,
		"status":     pod.Status.Phase,
		"ready":      fmt.Sprintf("%d/%d", readyCount, len(pod.Spec.Containers)),
		"restarts":   restarts,
		"node":       pod.Spec.NodeName,
		"ip":         pod.Status.PodIP,
		"containers": containers,
		"conditions": conditions,
		"createdAt":  pod.Metadata.CreationTimestamp.UTC().Format(time.RFC3339),
		"age":        humanAge(pod.Metadata.CreationTimestamp),
		"images":     images,
	}
}

func nodeSummaryMap(node struct {
	Metadata struct {
		Name              string            `json:"name"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Taints []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Effect string `json:"effect"`
		} `json:"taints"`
		Unschedulable bool `json:"unschedulable"`
	} `json:"spec"`
	Status struct {
		NodeInfo struct {
			KubeletVersion          string `json:"kubeletVersion"`
			OperatingSystem         string `json:"operatingSystem"`
			Architecture            string `json:"architecture"`
			ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
			MachineID               string `json:"machineID"`
			SystemUUID              string `json:"systemUUID"`
			BootID                  string `json:"bootID"`
			KernelVersion           string `json:"kernelVersion"`
			OSImage                 string `json:"osImage"`
			KubeProxyVersion        string `json:"kubeProxyVersion"`
		} `json:"nodeInfo"`
		Capacity map[string]string `json:"capacity"`
		Images   []struct {
			Names     []string `json:"names"`
			SizeBytes int64    `json:"sizeBytes"`
		} `json:"images"`
		Addresses []struct {
			Type    string `json:"type"`
			Address string `json:"address"`
		} `json:"addresses"`
		Conditions []struct {
			Type               string `json:"type"`
			Status             string `json:"status"`
			Reason             string `json:"reason"`
			Message            string `json:"message"`
			LastHeartbeatTime  string `json:"lastHeartbeatTime"`
			LastTransitionTime string `json:"lastTransitionTime"`
		} `json:"conditions"`
	} `json:"status"`
}, podCount int) map[string]any {
	conditions := make([]map[string]any, 0, len(node.Status.Conditions))
	for _, cond := range node.Status.Conditions {
		conditions = append(conditions, map[string]any{
			"type": cond.Type, "status": cond.Status, "reason": cond.Reason, "message": cond.Message, "lastTransition": cond.LastTransitionTime,
		})
	}
	return map[string]any{
		"name":              node.Metadata.Name,
		"status":            nodeStatus(node.Status.Conditions, node.Spec.Unschedulable),
		"roles":             nodeRoles(node.Metadata.Labels),
		"kubernetesVersion": node.Status.NodeInfo.KubeletVersion,
		"os":                node.Status.NodeInfo.OperatingSystem,
		"architecture":      node.Status.NodeInfo.Architecture,
		"containerRuntime":  node.Status.NodeInfo.ContainerRuntimeVersion,
		"cpuCapacity":       parseCPU(node.Status.Capacity["cpu"]),
		"cpuUsage":          0,
		"memoryCapacity":    parseMemory(node.Status.Capacity["memory"]),
		"memoryUsage":       0,
		"podCapacity":       parseInt(node.Status.Capacity["pods"]),
		"podCount":          podCount,
		"conditions":        conditions,
		"createdAt":         node.Metadata.CreationTimestamp.UTC().Format(time.RFC3339),
	}
}

func nodeStatus(conditions any, unschedulable bool) string {
	switch typed := conditions.(type) {
	case []map[string]string:
		for _, cond := range typed {
			if cond["type"] == "Ready" && cond["status"] == "True" {
				if unschedulable {
					return "SchedulingDisabled"
				}
				return "Ready"
			}
		}
	default:
		raw, _ := json.Marshal(typed)
		var generic []map[string]any
		if json.Unmarshal(raw, &generic) == nil {
			for _, cond := range generic {
				if cond["type"] == "Ready" && cond["status"] == "True" {
					if unschedulable {
						return "SchedulingDisabled"
					}
					return "Ready"
				}
			}
		}
	}
	return "NotReady"
}

func nodeRoles(labels map[string]string) []string {
	var roles []string
	for key := range labels {
		if strings.HasPrefix(key, "node-role.kubernetes.io/") {
			role := strings.TrimPrefix(key, "node-role.kubernetes.io/")
			if role == "" {
				role = "control-plane"
			}
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		roles = []string{"worker"}
	}
	sort.Strings(roles)
	return roles
}

func imagesToMaps(images []struct {
	Names     []string `json:"names"`
	SizeBytes int64    `json:"sizeBytes"`
}) []map[string]any {
	items := make([]map[string]any, 0, len(images))
	for _, image := range images {
		name := ""
		if len(image.Names) > 0 {
			name = image.Names[0]
		}
		items = append(items, map[string]any{"name": name, "sizeBytes": image.SizeBytes})
	}
	return items
}

func labelSelector(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func humanAge(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := time.Since(ts)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// parseCPU returns CPU in millicores. Accepts the standard k8s quantity
// strings: "100m" (millicores), "0.1" (cores → 100m), "4" (4000m), "4n"
// (nanocores → millicores), "1u" (microcores → millicores).
//
// The frontend formatCPU expects millicores universally; emitting cores here
// caused 4-core nodes to render as "4m" and 0.227-core usage as "0.227m".
func parseCPU(v string) int {
	quantity, err := resource.ParseQuantity(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return int(quantity.MilliValue())
}

func parseMemory(v string) int {
	quantity, err := resource.ParseQuantity(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return int(quantity.Value())
}

// nonNilSlice returns the slice if non-nil, else an empty slice of the same
// type. Frontend components iterate these fields and crash on JSON null —
// Go's typed nil slice marshals as `null` rather than `[]`.
func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// nonNilAny is the untyped equivalent for fields whose static type is
// []map[string]any or similar — tracker variables built ad hoc that may be
// nil before population.
func nonNilAny(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}

func valueOrOne(v *int32) int32 {
	if v == nil || *v == 0 {
		return 1
	}
	return *v
}

func defaultMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
