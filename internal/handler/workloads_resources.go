package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

func (h *WorkloadHandler) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	var namespaces namespaceList
	if err := h.getJSON(r.Context(), clusterID, "/api/v1/namespaces", &namespaces); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	pods, err := h.listPodMetadata(r.Context(), clusterID, "/api/v1/pods")
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	counts := map[string]int{}
	for _, pod := range pods.Items {
		counts[pod.Metadata.Namespace]++
	}
	items := make([]map[string]any, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		items = append(items, map[string]any{
			"name":        ns.Metadata.Name,
			"clusterId":   clusterID,
			"status":      ns.Status.Phase,
			"labels":      defaultMap(ns.Metadata.Labels),
			"annotations": defaultMap(ns.Metadata.Annotations),
			"podCount":    counts[ns.Metadata.Name],
			"cpuUsage":    0,
			"cpuLimit":    0,
			"memoryUsage": 0,
			"memoryLimit": 0,
			"createdAt":   ns.Metadata.CreationTimestamp.UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["name"].(string) < items[j]["name"].(string) })
	all, names, err := h.authz.authorizedNamespaces(r.Context(), clusterUUID, rbac.ResourceClusters, rbac.VerbRead)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !all {
		items = filterItemsByNamespaceKey(items, "name", names)
	}
	// Namespaces come straight from the cluster's API unpaginated; slice to the
	// requested page so Total reflects the full set and Next advances correctly.
	page, pagination := pageWindow(r, items)
	paging.Write(w, page, pagination)
}

func (h *WorkloadHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	nodes, err := h.getNodes(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	// Nodes come straight from the cluster's API unpaginated; slice to the
	// requested page so Total reflects the full set and Next advances correctly.
	page, pagination := pageWindow(r, nodes)
	paging.Write(w, page, pagination)
}

func (h *WorkloadHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	nodeName := chi.URLParam(r, "node_name")
	detail, err := h.getNodeDetail(r.Context(), clusterID, nodeName)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, detail)
}

func (h *WorkloadHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	var events eventList
	// Bound the upstream Kubernetes list as well as the response page. Passing
	// raw client input here allowed an arbitrarily large list to be buffered by
	// both the agent and server.
	path := "/api/v1/events?limit=" + strconv.Itoa(queryLimitMax(r, 100, 500))
	if err := h.getJSON(r.Context(), clusterID, path, &events); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(events.Items))
	for _, evt := range events.Items {
		items = append(items, map[string]any{
			"id":      evt.Metadata.UID,
			"type":    evt.Type,
			"reason":  evt.Reason,
			"message": evt.Message,
			"involvedObject": map[string]any{
				"kind":      evt.InvolvedObject.Kind,
				"name":      evt.InvolvedObject.Name,
				"namespace": evt.InvolvedObject.Namespace,
			},
			"count":          evt.Count,
			"firstTimestamp": evt.FirstTimestamp,
			"lastTimestamp":  evt.LastTimestamp,
		})
	}
	all, names, err := h.authz.authorizedNamespaces(r.Context(), clusterUUID, rbac.ResourceClusters, rbac.VerbRead)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !all {
		items = filterEventsByNamespace(items, names)
	}
	// Events come straight from the cluster's API (capped by the limit query
	// param the agent honours); slice to the requested page so Total reflects the
	// fetched set and Next advances correctly instead of re-serving the same rows.
	page, pagination := pageWindow(r, items)
	paging.Write(w, page, pagination)
}

func (h *WorkloadHandler) ListPods(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	namespace := r.URL.Query().Get("namespace")
	pods, err := h.listPods(r.Context(), clusterID, namespace, "")
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	all, names, err := h.authz.authorizedNamespaces(r.Context(), clusterUUID, rbac.ResourcePods, rbac.VerbList)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !all {
		pods = filterItemsByNamespaceKey(pods, "namespace", names)
	}
	// Pods come straight from the cluster's API unpaginated; slice to the
	// requested page so Total reflects the full set and Next advances correctly.
	page, pagination := pageWindow(r, pods)
	paging.Write(w, page, pagination)
}

func (h *WorkloadHandler) ListWorkloadPods(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	kind, namespace, name := chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	resource, err := h.fetchWorkloadResource(r.Context(), clusterID, kind, namespace, name)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	selector := labelSelector(resource.Spec.Selector.MatchLabels)
	pods, err := h.listPods(r.Context(), clusterID, namespace, selector)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	// Selector-scoped pods come straight from the cluster's API unpaginated;
	// slice to the requested page so Total reflects the full set and Next
	// advances correctly instead of re-serving the identical selector result.
	page, pagination := pageWindow(r, pods)
	paging.Write(w, page, pagination)
}

func (h *WorkloadHandler) DeletePod(w http.ResponseWriter, r *http.Request) {
	parsedClusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := parsedClusterID.String()
	namespace, pod := chi.URLParam(r, "namespace"), chi.URLParam(r, "pod")
	if len(k8svalidation.IsDNS1123Label(namespace)) != 0 || len(k8svalidation.IsDNS1123Subdomain(pod)) != 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "Invalid Kubernetes namespace or pod name")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Durable pod-delete storage is unavailable")
		return
	}

	opContext := withOperationIdempotency(r, "pod-deletes")
	var operation sqlc.WorkloadOperation
	err := h.runTx(r.Context(), func(q WorkloadMutationTx) error {
		idempotencyQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("durable pod-delete idempotency storage is unavailable")
		}
		operationQ, ok := q.(interface {
			GetWorkloadOperation(context.Context, uuid.UUID) (sqlc.WorkloadOperation, error)
		})
		if !ok {
			return errors.New("durable pod-delete operation storage is unavailable")
		}
		existingID, replay, claimErr := claimResourceOperation(opContext, idempotencyQ, "workload_operations")
		if claimErr != nil {
			return claimErr
		}
		if replay {
			operation, claimErr = operationQ.GetWorkloadOperation(r.Context(), existingID)
			if claimErr != nil {
				return claimErr
			}
			if operation.TargetType != "pod" || operation.TargetKey != workloadTargetKey(clusterID, "Pod", namespace, pod) || operation.OperationType != "delete_pod" {
				return errWorkloadOperationIdempotencyConflict
			}
			return nil
		}
		var createErr error
		operation, createErr = createWorkloadOperation(r.Context(), q, "pod",
			workloadTargetKey(clusterID, "Pod", namespace, pod), "delete_pod",
			workloadOperationEnvelope{ClusterID: clusterID, Kind: "Pod", Namespace: namespace, Name: pod}, currentUserUUID(r))
		if createErr != nil {
			return createErr
		}
		if createErr = attachResourceOperation(opContext, idempotencyQ, "workload_operations", operation.ID, workloadOperationResponse(operation)); createErr != nil {
			return createErr
		}
		task, taskErr := tasks.NewPodDeleteTask(operation.ID)
		if taskErr != nil {
			return taskErr
		}
		payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), reqctx.CorrelationID(r.Context()))
		task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(5), asynq.Timeout(2*time.Minute))
		if _, taskErr = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: "pod:delete:" + operation.ID.String(), QueueName: tasks.ClusterTemplateApplyQueueName,
			MaxRetry: 5, Timeout: 2 * time.Minute, MaxDeliveryAttempts: 20,
		}); taskErr != nil {
			return taskErr
		}
		return recordAuditOutbox(r, q, "pod.delete.requested", "pod", operation.ID.String(), pod,
			http.StatusAccepted, map[string]any{
				"cluster_id": parsedClusterID.String(), "namespace": namespace,
				"pod": pod, "operation_id": operation.ID.String(),
			})
	})
	if errors.Is(err, errWorkloadOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a different pod-delete operation")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.EnqueueError,
			"Failed to create pod-delete operation")
		return
	}
	operationURL := "/api/v1/workloads/operations/" + operation.ID.String() + "/"
	RespondAcceptedOperation(w, operationURL, workloadOperationResponse(operation))
}

func (h *WorkloadHandler) PodLogs(w http.ResponseWriter, r *http.Request) {
	clusterUUID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	clusterID := clusterUUID.String()
	namespace, pod := chi.URLParam(r, "namespace"), chi.URLParam(r, "pod")
	q := url.Values{}
	if c := r.URL.Query().Get("container"); c != "" {
		q.Set("container", c)
	}
	if t := r.URL.Query().Get("tailLines"); t != "" {
		q.Set("tailLines", t)
	} else if t := r.URL.Query().Get("tail_lines"); t != "" {
		q.Set("tailLines", t)
	}
	// Rancher-style time window: when sinceSeconds is set the UI is asking
	// "give me everything from the last N seconds" instead of "the last N
	// lines". Forward as the kubelet-native `sinceSeconds` param.
	if s := r.URL.Query().Get("sinceSeconds"); s != "" {
		q.Set("sinceSeconds", s)
	} else if s := r.URL.Query().Get("since_seconds"); s != "" {
		q.Set("sinceSeconds", s)
	}
	if f := r.URL.Query().Get("follow"); f != "" {
		q.Set("follow", f)
	}
	if previous := r.URL.Query().Get("previous"); previous == "true" {
		q.Set("previous", "true")
	}
	// Ask kubelet for timestamps so we can show real per-line times in the
	// UI instead of stamping every line with the response time.
	q.Set("timestamps", "true")
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log", namespace, pod)
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	resp, err := h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil || ensureSuccess(resp) != nil {
		if err == nil {
			err = ensureSuccess(resp)
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	body, _ := decodeResponseBody(resp)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	items := make([]map[string]any, 0, len(lines))
	fallback := time.Now().UTC().Format(time.RFC3339Nano)
	container := r.URL.Query().Get("container")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ts := fallback
		msg := line
		// Kubelet emits "<RFC3339Nano> <message>" when timestamps=true. Split
		// the prefix off so the frontend can show real log times; fall back
		// to the response time if the line doesn't carry a parseable prefix
		// (e.g. older clusters, multi-line scanner artifacts).
		if sp := strings.IndexByte(line, ' '); sp > 0 {
			if _, err := time.Parse(time.RFC3339Nano, line[:sp]); err == nil {
				ts = line[:sp]
				msg = line[sp+1:]
			}
		}
		items = append(items, map[string]any{
			"timestamp": ts,
			"message":   msg,
			"container": container,
		})
	}
	RespondJSON(w, http.StatusOK, items)
}
