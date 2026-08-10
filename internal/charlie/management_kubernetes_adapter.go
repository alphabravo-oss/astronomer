package charlie

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
)

const maxManagementLogBytes = 64 << 10

type ManagementKubernetesAdapter struct {
	kube      kubernetes.Interface
	namespace string
	release   string
	logStream func(context.Context, string, string, int64) (io.ReadCloser, error)
}

func NewManagementKubernetesAdapter(kube kubernetes.Interface, namespace, release string) (*ManagementKubernetesAdapter, error) {
	if kube == nil || len(utilvalidation.IsDNS1123Label(namespace)) != 0 || len(utilvalidation.IsDNS1123Label(release)) != 0 {
		return nil, fmt.Errorf("Charlie management Kubernetes adapter is unavailable")
	}
	adapter := &ManagementKubernetesAdapter{kube: kube, namespace: namespace, release: release}
	adapter.logStream = func(ctx context.Context, pod, container string, lines int64) (io.ReadCloser, error) {
		return kube.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{Container: container, TailLines: &lines, LimitBytes: ptrInt64(maxManagementLogBytes)}).Stream(ctx)
	}
	return adapter, nil
}

func (a *ManagementKubernetesAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	switch capability.Name {
	case "astronomer.management.workloads":
		return a.workloads(ctx, capability, arguments)
	case "astronomer.management.workload_get":
		kind, name, err := workloadArgument(arguments)
		if err != nil {
			return nil, err
		}
		return a.workload(ctx, capability, kind, name)
	case "astronomer.management.pods":
		return a.pods(ctx, capability, arguments)
	case "astronomer.management.rollout_status":
		kind, name, err := workloadArgument(arguments)
		if err != nil {
			return nil, err
		}
		return a.rolloutStatus(ctx, capability, kind, name)
	case "astronomer.management.events":
		return a.events(ctx, capability, arguments)
	case "astronomer.management.pod_logs":
		return a.logs(ctx, capability, arguments)
	case "astronomer.management.nodes":
		return a.nodes(ctx, capability)
	case "astronomer.management.storage":
		return a.storage(ctx, capability)
	case "astronomer.management.network":
		return a.network(ctx, capability)
	case "astronomer.management.workload_restart", "astronomer.management.workload_rollout", "astronomer.tunnel.restart_component":
		name, err := a.mutableDeploymentName(arguments, capability.Name)
		if err != nil {
			return nil, err
		}
		return a.rollout(ctx, capability, name, stringArgument(arguments, "operation_id"))
	case "astronomer.management.workload_scale":
		_, name, err := workloadArgument(arguments)
		if err != nil {
			return nil, err
		}
		if !a.mutableName(name) {
			return nil, fmt.Errorf("management workload is not mutable")
		}
		replicas := int32(int64Argument(arguments, "replicas", 0))
		deployment, err := a.kube.AppsV1().Deployments(a.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if !a.owned(deployment.Labels, deployment.Name) {
			return nil, fmt.Errorf("management workload ownership changed")
		}
		deployment.Spec.Replicas = &replicas
		updated, err := a.kube.AppsV1().Deployments(a.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return nil, err
		}
		return marshalBounded(operationResult(arguments, "accepted", "deployment/"+updated.Name), capability.MaxResponseBytes)
	case "astronomer.management.run_job":
		return a.runJob(ctx, capability, arguments)
	default:
		return nil, fmt.Errorf("unsupported management Kubernetes capability")
	}
}

func (a *ManagementKubernetesAdapter) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, result json.RawMessage) (bool, error) {
	if capability.Effect == EffectRead {
		return true, nil
	}
	if capability.Name == "astronomer.management.run_job" {
		var operation struct {
			Target string `json:"target"`
		}
		if json.Unmarshal(result, &operation) != nil || !strings.HasPrefix(operation.Target, "job/") {
			return false, nil
		}
		job, err := a.kube.BatchV1().Jobs(a.namespace).Get(ctx, strings.TrimPrefix(operation.Target, "job/"), metav1.GetOptions{})
		return err == nil && a.owned(job.Labels, job.Name), err
	}
	name, err := a.mutableDeploymentName(arguments, capability.Name)
	if capability.Name == "astronomer.management.workload_scale" {
		_, name, err = workloadArgument(arguments)
	}
	if err != nil {
		return false, err
	}
	deployment, err := a.kube.AppsV1().Deployments(a.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if capability.Name == "astronomer.management.workload_scale" {
		return deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == int32(int64Argument(arguments, "replicas", 0)), nil
	}
	var receipt struct {
		OperationID  string      `json:"operation_id"`
		Target       string      `json:"target"`
		PriorPodUIDs []types.UID `json:"prior_pod_uids"`
	}
	if json.Unmarshal(result, &receipt) != nil || receipt.OperationID != stringArgument(arguments, "operation_id") ||
		receipt.Target != "deployment/"+name || len(receipt.PriorPodUIDs) == 0 {
		return false, nil
	}
	pods, err := a.deploymentPods(ctx, deployment)
	if err != nil {
		return false, err
	}
	prior := make(map[types.UID]struct{}, len(receipt.PriorPodUIDs))
	for _, uid := range receipt.PriorPodUIDs {
		if uid == "" {
			return false, nil
		}
		prior[uid] = struct{}{}
	}
	ready := int32(0)
	replacementSeen := false
	for _, pod := range pods {
		if _, wasPresent := prior[pod.UID]; wasPresent {
			return false, nil
		}
		if podReady(&pod) {
			ready++
			replacementSeen = true
		}
	}
	return replacementSeen && deployment.Spec.Replicas != nil && ready >= *deployment.Spec.Replicas, nil
}

func (a *ManagementKubernetesAdapter) runJob(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	jobKind := stringArgument(arguments, "job")
	cronJobName := a.release + "-" + jobKind
	cronJob, err := a.kube.BatchV1().CronJobs(a.namespace).Get(ctx, cronJobName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(cronJob.Labels, cronJob.Name) {
		return nil, fmt.Errorf("maintenance job ownership changed")
	}
	labels := map[string]string{"app.kubernetes.io/instance": a.release, "astronomer.io/charlie-operation": stringArgument(arguments, "operation_id")}
	for key, value := range cronJob.Spec.JobTemplate.Labels {
		if strings.HasPrefix(key, "app.kubernetes.io/") || strings.HasPrefix(key, "astronomer.io/") {
			labels[key] = value
		}
	}
	if len(cronJobName) > 43 {
		return nil, fmt.Errorf("maintenance job name exceeds safe bound")
	}
	operationDigest := digestBytes([]byte(stringArgument(arguments, "operation_id")))
	jobName := cronJobName + "-charlie-" + operationDigest[:12]
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: a.namespace, Labels: labels},
		Spec:       *cronJob.Spec.JobTemplate.Spec.DeepCopy(),
	}
	created, err := a.kube.BatchV1().Jobs(a.namespace).Create(ctx, job, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = a.kube.BatchV1().Jobs(a.namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err == nil && created.Labels["astronomer.io/charlie-operation"] != stringArgument(arguments, "operation_id") {
			return nil, fmt.Errorf("maintenance job idempotency conflict")
		}
	}
	if err != nil {
		return nil, err
	}
	return marshalBounded(operationResult(arguments, "accepted", "job/"+created.Name), capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) workloads(ctx context.Context, capability CapabilityDescriptor, args map[string]json.RawMessage) (json.RawMessage, error) {
	deployments, err := a.kube.AppsV1().Deployments(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	statefulSets, err := a.kube.AppsV1().StatefulSets(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := a.kube.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, item := range deployments.Items {
		if a.owned(item.Labels, item.Name) {
			summary := deploymentSummary(item)
			addPodHealth(summary, pods.Items, item.Name)
			items = append(items, summary)
		}
	}
	for _, item := range statefulSets.Items {
		if a.owned(item.Labels, item.Name) {
			summary := statefulSetSummary(item)
			addPodHealth(summary, pods.Items, item.Name)
			items = append(items, summary)
		}
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["workload"]) < fmt.Sprint(items[j]["workload"]) })
	page, size := pagination(args, 50)
	start := int((page - 1) * size)
	if start > len(items) {
		start = len(items)
	}
	end := min(start+int(size), len(items))
	return marshalBounded(map[string]any{"items": items[start:end], "page": page, "page_size": size}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) workload(ctx context.Context, capability CapabilityDescriptor, kind, name string) (json.RawMessage, error) {
	if kind == "deployment" {
		item, err := a.kube.AppsV1().Deployments(a.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if !a.owned(item.Labels, item.Name) {
			return nil, fmt.Errorf("workload is outside Astronomer")
		}
		summary := deploymentSummary(*item)
		pods, err := a.kube.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		addPodHealth(summary, pods.Items, item.Name)
		return marshalBounded(summary, capability.MaxResponseBytes)
	}
	item, err := a.kube.AppsV1().StatefulSets(a.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(item.Labels, item.Name) {
		return nil, fmt.Errorf("workload is outside Astronomer")
	}
	summary := statefulSetSummary(*item)
	pods, err := a.kube.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	addPodHealth(summary, pods.Items, item.Name)
	return marshalBounded(summary, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) pods(ctx context.Context, capability CapabilityDescriptor, args map[string]json.RawMessage) (json.RawMessage, error) {
	rows, err := a.kube.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	component := stringArgument(args, "component")
	phaseFilter := stringArgument(args, "phase")
	items := []map[string]any{}
	for _, pod := range rows.Items {
		if !a.owned(pod.Labels, pod.Name) {
			continue
		}
		if component != "" && !strings.Contains(pod.Name, component) {
			continue
		}
		if phaseFilter != "" && string(pod.Status.Phase) != phaseFilter {
			continue
		}
		ready := false
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		restarts := int32(0)
		containers := make([]map[string]any, 0, len(pod.Spec.Containers))
		for _, c := range pod.Spec.Containers {
			restartCount := int32(0)
			state := "unknown"
			for _, st := range pod.Status.ContainerStatuses {
				if st.Name == c.Name {
					restartCount = st.RestartCount
					restarts += st.RestartCount
					switch {
					case st.State.Running != nil:
						state = "running"
					case st.State.Waiting != nil:
						state = st.State.Waiting.Reason
						if state == "" {
							state = "waiting"
						}
					case st.State.Terminated != nil:
						state = st.State.Terminated.Reason
						if state == "" {
							state = "terminated"
						}
					}
				}
			}
			containers = append(containers, map[string]any{"name": c.Name, "state": state, "restarts": restartCount})
		}
		owner := ""
		for _, ref := range pod.OwnerReferences {
			if ref.Controller != nil && *ref.Controller {
				owner = strings.ToLower(ref.Kind) + "/" + ref.Name
				break
			}
		}
		items = append(items, map[string]any{
			"name": pod.Name, "phase": pod.Status.Phase, "ready": ready, "restarts": restarts,
			"node": pod.Spec.NodeName, "owner": owner, "containers": containers,
		})
	}
	sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["name"]) < fmt.Sprint(items[j]["name"]) })
	page, size := pagination(args, 50)
	start := int((page - 1) * size)
	if start > len(items) {
		start = len(items)
	}
	end := min(start+int(size), len(items))
	return marshalBounded(map[string]any{"items": items[start:end], "page": page, "page_size": size, "total": len(items)}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) rolloutStatus(ctx context.Context, capability CapabilityDescriptor, kind, name string) (json.RawMessage, error) {
	if kind == "deployment" {
		item, err := a.kube.AppsV1().Deployments(a.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if !a.owned(item.Labels, item.Name) {
			return nil, fmt.Errorf("workload is outside Astronomer")
		}
		desired := int32Value(item.Spec.Replicas)
		complete := item.Status.UpdatedReplicas >= desired && item.Status.ReadyReplicas >= desired &&
			item.Status.AvailableReplicas >= desired && item.Status.ObservedGeneration >= item.Generation
		progressing, available := "", ""
		for _, c := range item.Status.Conditions {
			switch c.Type {
			case appsv1.DeploymentProgressing:
				progressing = string(c.Status) + ":" + c.Reason
			case appsv1.DeploymentAvailable:
				available = string(c.Status) + ":" + c.Reason
			}
		}
		return marshalBounded(map[string]any{
			"workload": "deployment/" + item.Name, "complete": complete,
			"desired": desired, "ready": item.Status.ReadyReplicas, "updated": item.Status.UpdatedReplicas,
			"available": item.Status.AvailableReplicas, "unavailable": item.Status.UnavailableReplicas,
			"generation": item.Generation, "observed_generation": item.Status.ObservedGeneration,
			"progressing": progressing, "available_condition": available,
		}, capability.MaxResponseBytes)
	}
	item, err := a.kube.AppsV1().StatefulSets(a.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(item.Labels, item.Name) {
		return nil, fmt.Errorf("workload is outside Astronomer")
	}
	desired := int32Value(item.Spec.Replicas)
	complete := item.Status.ReadyReplicas >= desired && item.Status.UpdatedReplicas >= desired &&
		item.Status.ObservedGeneration >= item.Generation
	return marshalBounded(map[string]any{
		"workload": "statefulset/" + item.Name, "complete": complete,
		"desired": desired, "ready": item.Status.ReadyReplicas, "current": item.Status.CurrentReplicas,
		"updated": item.Status.UpdatedReplicas, "generation": item.Generation,
		"observed_generation": item.Status.ObservedGeneration,
	}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) events(ctx context.Context, capability CapabilityDescriptor, args map[string]json.RawMessage) (json.RawMessage, error) {
	rows, err := a.kube.CoreV1().Events(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	component := stringArgument(args, "component")
	limit := int(int64Argument(args, "limit", 100))
	since := sinceArgument(args, time.Hour)
	items := []map[string]any{}
	for _, event := range rows.Items {
		when := event.EventTime.Time
		if when.IsZero() {
			when = event.LastTimestamp.Time
		}
		if when.Before(since) {
			continue
		}
		if component != "" && event.InvolvedObject.Name != component {
			continue
		}
		if !a.nameOwned(event.InvolvedObject.Name) {
			continue
		}
		items = append(items, map[string]any{"type": event.Type, "reason": event.Reason, "component": event.InvolvedObject.Name, "count": event.Count, "occurred_at": when.UTC()})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["occurred_at"].(time.Time).After(items[j]["occurred_at"].(time.Time))
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return marshalBounded(map[string]any{"items": items}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) logs(ctx context.Context, capability CapabilityDescriptor, args map[string]json.RawMessage) (json.RawMessage, error) {
	podName, container := stringArgument(args, "pod"), stringArgument(args, "container")
	pod, err := a.kube.CoreV1().Pods(a.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(pod.Labels, pod.Name) || !podHasContainer(pod, container) {
		return nil, fmt.Errorf("pod or container is outside Astronomer")
	}
	lines := int64Argument(args, "lines", 200)
	stream, err := a.logStream(ctx, podName, container, lines)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	bounded, err := io.ReadAll(io.LimitReader(stream, maxManagementLogBytes+1))
	if err != nil || len(bounded) > maxManagementLogBytes {
		return nil, fmt.Errorf("management logs exceed bound")
	}
	return marshalBounded(map[string]any{"pod": podName, "container": container, "lines": redactLogLines(bounded, int(lines))}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) nodes(ctx context.Context, capability CapabilityDescriptor) (json.RawMessage, error) {
	rows, err := a.kube.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	serverVersion := "unavailable"
	if info, versionErr := a.kube.Discovery().ServerVersion(); versionErr == nil && info != nil {
		serverVersion = info.GitVersion
	}
	items := make([]map[string]any, 0, len(rows.Items))
	for _, node := range rows.Items {
		conditions := map[string]string{}
		for _, c := range node.Status.Conditions {
			if c.Type == corev1.NodeReady || c.Type == corev1.NodeMemoryPressure || c.Type == corev1.NodeDiskPressure || c.Type == corev1.NodePIDPressure {
				conditions[string(c.Type)] = string(c.Status)
			}
		}
		items = append(items, map[string]any{
			"name": node.Name, "capacity_cpu": node.Status.Capacity.Cpu().String(),
			"capacity_memory": node.Status.Capacity.Memory().String(), "conditions": conditions,
			"kubelet_version": node.Status.NodeInfo.KubeletVersion,
			"os_image":        node.Status.NodeInfo.OSImage,
			"architecture":    node.Status.NodeInfo.Architecture,
		})
	}
	return marshalBounded(map[string]any{"server_version": serverVersion, "items": items}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) storage(ctx context.Context, capability CapabilityDescriptor) (json.RawMessage, error) {
	rows, err := a.kube.CoreV1().PersistentVolumeClaims(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, pvc := range rows.Items {
		if a.owned(pvc.Labels, pvc.Name) {
			items = append(items, map[string]any{"name": pvc.Name, "phase": pvc.Status.Phase, "capacity": pvc.Status.Capacity.Storage().String(), "access_modes": pvc.Spec.AccessModes})
		}
	}
	return marshalBounded(map[string]any{"items": items}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) network(ctx context.Context, capability CapabilityDescriptor) (json.RawMessage, error) {
	services, err := a.kube.CoreV1().Services(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	ingresses, err := a.kube.NetworkingV1().Ingresses(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	policies, err := a.kube.NetworkingV1().NetworkPolicies(a.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	serviceItems := []map[string]any{}
	for _, s := range services.Items {
		if a.owned(s.Labels, s.Name) {
			ports := []map[string]any{}
			for _, p := range s.Spec.Ports {
				ports = append(ports, map[string]any{"name": p.Name, "port": p.Port, "target_port": targetPort(p.TargetPort)})
			}
			serviceItems = append(serviceItems, map[string]any{"name": s.Name, "type": s.Spec.Type, "ports": ports})
		}
	}
	ingressItems := []map[string]any{}
	for _, v := range ingresses.Items {
		if a.owned(v.Labels, v.Name) {
			ingressItems = append(ingressItems, map[string]any{"name": v.Name, "rules": len(v.Spec.Rules), "load_balancer_ready": len(v.Status.LoadBalancer.Ingress) > 0})
		}
	}
	policyItems := []map[string]any{}
	for _, v := range policies.Items {
		if a.owned(v.Labels, v.Name) {
			policyItems = append(policyItems, map[string]any{"name": v.Name, "policy_types": v.Spec.PolicyTypes})
		}
	}
	return marshalBounded(map[string]any{"services": serviceItems, "ingresses": ingressItems, "network_policies": policyItems}, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) rollout(ctx context.Context, capability CapabilityDescriptor, name, operationID string) (json.RawMessage, error) {
	d, err := a.kube.AppsV1().Deployments(a.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !a.owned(d.Labels, d.Name) || !a.mutableName(d.Name) {
		return nil, fmt.Errorf("management workload is not mutable")
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas < 2 {
		return nil, fmt.Errorf("management workload lacks safe redundancy")
	}
	pods, err := a.deploymentPods(ctx, d)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("management workload has no controlled pods")
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	targets := pods
	unhealthyOnly := capability.Name == "astronomer.management.workload_restart" || capability.Name == "astronomer.tunnel.restart_component"
	if unhealthyOnly {
		targets = nil
		for i := range pods {
			if !podReady(&pods[i]) {
				targets = append(targets, pods[i])
				break
			}
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("management workload is healthy; restart is not justified")
		}
	} else {
		ready := int32(0)
		active := 0
		for i := range pods {
			if pods[i].DeletionTimestamp == nil {
				active++
			}
			if podReady(&pods[i]) {
				ready++
			}
		}
		if active != int(*d.Spec.Replicas) || ready != *d.Spec.Replicas || d.Status.ObservedGeneration < d.Generation {
			return nil, fmt.Errorf("management workload is not in a safe steady state for rollout")
		}
	}
	priorUIDs := make([]types.UID, 0, len(targets))
	for i := range targets {
		priorUIDs = append(priorUIDs, targets[i].UID)
		if unhealthyOnly {
			uid := targets[i].UID
			if err := a.kube.CoreV1().Pods(a.namespace).Delete(ctx, targets[i].Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
				return nil, fmt.Errorf("replace unhealthy management pod: %w", err)
			}
		} else {
			if err := a.evictWhenBudgetAllows(ctx, &targets[i]); err != nil {
				return nil, fmt.Errorf("safely evict management pod: %w", err)
			}
		}
		if err := a.waitForReplacement(ctx, d, targets[i].UID); err != nil {
			return nil, err
		}
	}
	result := operationResult(argumentsWithOperation(operationID), "accepted", "deployment/"+d.Name)
	result["prior_pod_uids"] = priorUIDs
	result["replaced_pods"] = len(priorUIDs)
	return marshalBounded(result, capability.MaxResponseBytes)
}

func (a *ManagementKubernetesAdapter) evictWhenBudgetAllows(ctx context.Context, pod *corev1.Pod) error {
	if pod == nil || pod.UID == "" {
		return fmt.Errorf("management pod identity is unavailable")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := a.kube.CoreV1().Pods(a.namespace).EvictV1(ctx, &policyv1.Eviction{
			ObjectMeta:    metav1.ObjectMeta{Name: pod.Name, Namespace: a.namespace},
			DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &pod.UID}},
		})
		if err == nil {
			return nil
		}
		if !apierrors.IsTooManyRequests(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *ManagementKubernetesAdapter) deploymentPods(ctx context.Context, deployment *appsv1.Deployment) ([]corev1.Pod, error) {
	if deployment == nil || deployment.Spec.Selector == nil {
		return nil, fmt.Errorf("management workload selector is unavailable")
	}
	selector := metav1.FormatLabelSelector(deployment.Spec.Selector)
	replicaSets, err := a.kube.AppsV1().ReplicaSets(a.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	ownedReplicaSets := make(map[types.UID]struct{}, len(replicaSets.Items))
	for i := range replicaSets.Items {
		if controlledBy(&replicaSets.Items[i], deployment.UID, "Deployment", deployment.Name) {
			ownedReplicaSets[replicaSets.Items[i].UID] = struct{}{}
		}
	}
	if len(ownedReplicaSets) == 0 {
		return nil, fmt.Errorf("management workload controller ownership is unavailable")
	}
	listed, err := a.kube.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	pods := make([]corev1.Pod, 0, len(listed.Items))
	for i := range listed.Items {
		for _, owner := range listed.Items[i].OwnerReferences {
			if owner.Controller != nil && *owner.Controller && owner.Kind == "ReplicaSet" {
				if _, ok := ownedReplicaSets[owner.UID]; ok {
					pods = append(pods, listed.Items[i])
				}
			}
		}
	}
	return pods, nil
}

func (a *ManagementKubernetesAdapter) waitForReplacement(ctx context.Context, deployment *appsv1.Deployment, priorUID types.UID) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		pods, err := a.deploymentPods(ctx, deployment)
		if err != nil {
			return err
		}
		ready := int32(0)
		priorPresent := false
		for i := range pods {
			priorPresent = priorPresent || pods[i].UID == priorUID
			if podReady(&pods[i]) {
				ready++
			}
		}
		if !priorPresent && deployment.Spec.Replicas != nil && ready >= *deployment.Spec.Replicas {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("management pod replacement did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func controlledBy(object metav1.Object, ownerUID types.UID, ownerKind, ownerName string) bool {
	for _, owner := range object.GetOwnerReferences() {
		if owner.Controller != nil && *owner.Controller && owner.UID == ownerUID && owner.Kind == ownerKind && owner.Name == ownerName {
			return true
		}
	}
	return false
}

func (a *ManagementKubernetesAdapter) mutableDeploymentName(args map[string]json.RawMessage, capability string) (string, error) {
	if capability == "astronomer.tunnel.restart_component" {
		name := a.release + "-" + stringArgument(args, "component")
		if !a.mutableName(name) {
			return "", fmt.Errorf("component is not mutable")
		}
		return name, nil
	}
	kind, name, err := workloadArgument(args)
	if err != nil || kind != "deployment" || !a.mutableName(name) {
		return "", fmt.Errorf("management workload is not mutable")
	}
	return name, nil
}
func (a *ManagementKubernetesAdapter) mutableName(name string) bool {
	return name == a.release+"-server" || name == a.release+"-worker" || name == a.release+"-frontend"
}
func (a *ManagementKubernetesAdapter) nameOwned(name string) bool {
	return name == a.release || strings.HasPrefix(name, a.release+"-")
}
func (a *ManagementKubernetesAdapter) owned(labels map[string]string, name string) bool {
	return a.nameOwned(name) && (labels["app.kubernetes.io/instance"] == "" || labels["app.kubernetes.io/instance"] == a.release)
}

func workloadArgument(args map[string]json.RawMessage) (string, string, error) {
	value := stringArgument(args, "workload")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || (parts[0] != "deployment" && parts[0] != "statefulset") {
		return "", "", fmt.Errorf("workload identifier is invalid")
	}
	return parts[0], parts[1], nil
}
func stringArgument(args map[string]json.RawMessage, name string) string {
	var value string
	_ = json.Unmarshal(args[name], &value)
	return value
}
func deploymentSummary(item appsv1.Deployment) map[string]any {
	return map[string]any{"workload": "deployment/" + item.Name, "desired": int32Value(item.Spec.Replicas), "ready": item.Status.ReadyReplicas, "available": item.Status.AvailableReplicas, "updated": item.Status.UpdatedReplicas, "generation": item.Generation, "observed_generation": item.Status.ObservedGeneration}
}
func statefulSetSummary(item appsv1.StatefulSet) map[string]any {
	return map[string]any{"workload": "statefulset/" + item.Name, "desired": int32Value(item.Spec.Replicas), "ready": item.Status.ReadyReplicas, "current": item.Status.CurrentReplicas, "updated": item.Status.UpdatedReplicas, "generation": item.Generation, "observed_generation": item.Status.ObservedGeneration}
}
func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}
func addPodHealth(summary map[string]any, pods []corev1.Pod, workload string) {
	restarts, oomKills, unready := int32(0), int32(0), int32(0)
	for _, pod := range pods {
		if !strings.HasPrefix(pod.Name, workload+"-") {
			continue
		}
		podReady := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				podReady = true
			}
		}
		if !podReady {
			unready++
		}
		for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			restarts += status.RestartCount
			if status.LastTerminationState.Terminated != nil && status.LastTerminationState.Terminated.Reason == "OOMKilled" {
				oomKills++
			}
		}
	}
	summary["pod_restarts"], summary["oom_kills"], summary["unready_pods"] = restarts, oomKills, unready
}
func podHasContainer(pod *corev1.Pod, name string) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	return false
}
func ptrInt64(value int64) *int64 { return &value }
func targetPort(value intstr.IntOrString) any {
	if value.Type == intstr.String {
		return value.StrVal
	}
	return value.IntVal
}
func operationResult(args map[string]json.RawMessage, state, target string) map[string]any {
	return map[string]any{"operation_id": stringArgument(args, "operation_id"), "state": state, "target": target, "status_path": "/api/v1/charlie/operations/" + stringArgument(args, "operation_id")}
}
func argumentsWithOperation(value string) map[string]json.RawMessage {
	encoded, _ := json.Marshal(value)
	return map[string]json.RawMessage{"operation_id": encoded}
}

var sensitiveLogPattern = regexp.MustCompile(`(?i)(authorization|password|passwd|secret|token|api[_-]?key|private[_-]?key)\s*[:=]\s*[^\s]+`)

func redactLogLines(raw []byte, maxLines int) []string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maxManagementLogBytes)
	lines := []string{}
	for scanner.Scan() && len(lines) < maxLines {
		line := sensitiveLogPattern.ReplaceAllString(scanner.Text(), "$1=[REDACTED]")
		if len(line) > 2048 {
			line = line[:2048]
		}
		lines = append(lines, line)
	}
	return lines
}
