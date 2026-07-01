package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/operationstate"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type WorkloadHandler struct {
	requester K8sRequester
	queries   WorkloadQuerier
	log       *slog.Logger
	authz     authorizationSupport
	mu        sync.Mutex
	trigger   chan struct{}
	// helmConcurrency caps the parallel dispatch fan-out for
	// executeOperation; zero falls back to the package default.
	helmConcurrency int
	// metrics is the shared cluster-metrics provider used to populate per-node
	// CPU/memory usage on the node-detail response. Nil-safe — when unset the
	// node detail still serves capacity-only data.
	metrics *clustermetrics.Provider
	// localClusterID identifies the singleton is_local=true cluster row so the
	// handler can pick the in-process fast path when the request targets it.
	// Empty when the local cluster hasn't been bootstrapped yet.
	localClusterID string
	// podWatcher backs the WatchPods SSE endpoint. Optional; nil makes
	// WatchPods return 501.
	podWatcher PodWatcher
}

func NewWorkloadHandler() *WorkloadHandler {
	return &WorkloadHandler{log: slog.Default(), trigger: make(chan struct{}, 1)}
}

func NewWorkloadHandlerWithRequester(requester K8sRequester) *WorkloadHandler {
	return &WorkloadHandler{requester: requester, log: slog.Default(), trigger: make(chan struct{}, 1)}
}

func NewWorkloadHandlerWithDeps(queries WorkloadQuerier, requester K8sRequester) *WorkloadHandler {
	return &WorkloadHandler{queries: queries, requester: requester, log: slog.Default(), trigger: make(chan struct{}, 1)}
}

type WorkloadQuerier interface {
	CreateWorkloadOperation(ctx context.Context, arg sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error)
	GetWorkloadOperation(ctx context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error)
	ListWorkloadOperations(ctx context.Context, arg sqlc.ListWorkloadOperationsParams) ([]sqlc.WorkloadOperation, error)
	CountWorkloadOperationsByStatus(ctx context.Context) ([]sqlc.CountWorkloadOperationsByStatusRow, error)
	ListPendingWorkloadOperations(ctx context.Context, limit int32) ([]sqlc.WorkloadOperation, error)
	MarkWorkloadOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationFailed(ctx context.Context, arg sqlc.MarkWorkloadOperationFailedParams) (sqlc.WorkloadOperation, error)
	MarkWorkloadOperationSuperseded(ctx context.Context, arg sqlc.MarkWorkloadOperationSupersededParams) (sqlc.WorkloadOperation, error)
	RequeueWorkloadOperation(ctx context.Context, id uuid.UUID) (sqlc.WorkloadOperation, error)
	CreateWorkloadOperationEvent(ctx context.Context, arg sqlc.CreateWorkloadOperationEventParams) (sqlc.WorkloadOperationEvent, error)
	ListWorkloadOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.WorkloadOperationEvent, error)
}

type workloadOperationEnvelope struct {
	ClusterID string `json:"clusterId"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas,omitempty"`
}

func (h *WorkloadHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

// SetMetricsProvider wires the shared metrics aggregator. The provider is the
// same instance the cluster handler uses, so node / pod usage stays cache-
// coherent across both endpoints.
func (h *WorkloadHandler) SetMetricsProvider(p *clustermetrics.Provider) {
	if h == nil {
		return
	}
	h.metrics = p
}

// SetLocalClusterID tells the handler which cluster ID is the in-process
// management cluster. The node-detail handler uses this to pick the fast
// path (in-cluster k8s client) instead of round-tripping through the tunnel.
func (h *WorkloadHandler) SetLocalClusterID(id string) {
	if h == nil {
		return
	}
	h.localClusterID = id
}

func (h *WorkloadHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

// SetNamespaceScopedRBAC toggles per-namespace filtering of the list handlers
// (pods/namespaces/events/workloads). Wired from the
// namespace_scoped_rbac_enabled config flag; default false leaves list
// responses byte-identical to the pre-feature behavior.
func (h *WorkloadHandler) SetNamespaceScopedRBAC(enabled bool) {
	if h == nil {
		return
	}
	h.authz.SetNamespaceScoped(enabled)
}

// parseClusterUUID parses the cluster_id route param into a uuid. On a malformed
// value it returns the zero UUID, which no cluster binding matches — so a
// namespace-scoped caller fails closed (empty allow-set) rather than seeing
// everything.
func parseClusterUUID(id string) uuid.UUID {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}
	}
	return parsed
}

// filterItemsByNamespaceKey returns only the items whose namespace (read from
// map key `key`) is in the allow-set. Strict allow-list: an item whose key is
// missing, non-string, or empty is dropped (fail closed) — a namespace-scoped
// caller never sees cluster-scoped or unlabeled objects.
func filterItemsByNamespaceKey(items []map[string]any, key string, allowed map[string]struct{}) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		ns, ok := item[key].(string)
		if !ok || ns == "" {
			continue
		}
		if _, permitted := allowed[ns]; permitted {
			out = append(out, item)
		}
	}
	return out
}

// filterEventsByNamespace mirrors filterItemsByNamespaceKey but reads the
// namespace from the nested involvedObject.namespace field.
func filterEventsByNamespace(items []map[string]any, allowed map[string]struct{}) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		obj, ok := item["involvedObject"].(map[string]any)
		if !ok {
			continue
		}
		ns, ok := obj["namespace"].(string)
		if !ok || ns == "" {
			continue
		}
		if _, permitted := allowed[ns]; permitted {
			out = append(out, item)
		}
	}
	return out
}

func (h *WorkloadHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *WorkloadHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *WorkloadHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

type workloadResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int32 `json:"replicas,omitempty"`
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		Replicas          int32 `json:"replicas"`
		ReadyReplicas     int32 `json:"readyReplicas"`
		UpdatedReplicas   int32 `json:"updatedReplicas"`
		AvailableReplicas int32 `json:"availableReplicas"`
		Succeeded         int32 `json:"succeeded"`
		Active            int32 `json:"active"`
		Conditions        []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

type workloadList struct {
	Items []workloadResource `json:"items"`
}

type podResource struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
		Labels            map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
			Ports []struct {
				Name          string `json:"name"`
				ContainerPort int    `json:"containerPort"`
				Protocol      string `json:"protocol"`
			} `json:"ports"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		PodIP             string `json:"podIP"`
		ContainerStatuses []struct {
			Name         string `json:"name"`
			Ready        bool   `json:"ready"`
			RestartCount int    `json:"restartCount"`
			State        struct {
				Running    any `json:"running"`
				Waiting    any `json:"waiting"`
				Terminated any `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
		Conditions []struct {
			Type               string `json:"type"`
			Status             string `json:"status"`
			Reason             string `json:"reason"`
			Message            string `json:"message"`
			LastTransitionTime string `json:"lastTransitionTime"`
		} `json:"conditions"`
	} `json:"status"`
}

type podList struct {
	Items []podResource `json:"items"`
}

// podMetadataList decodes a PartialObjectMetadataList (or a full PodList — both
// carry metadata.namespace). Used by callers that only need per-namespace or
// per-node pod counts, so they don't pull every pod's spec+status.
type podMetadataList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	} `json:"items"`
}

type namespaceList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
		} `json:"metadata"`
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	} `json:"items"`
}

type nodeList struct {
	Items []struct {
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
	} `json:"items"`
}

type eventList struct {
	Items []struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
		Type           string `json:"type"`
		Reason         string `json:"reason"`
		Message        string `json:"message"`
		InvolvedObject struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"involvedObject"`
		Count          int32  `json:"count"`
		FirstTimestamp string `json:"firstTimestamp"`
		LastTimestamp  string `json:"lastTimestamp"`
	} `json:"items"`
}

// pageWindow slices the fully-materialized cluster resource list to the
// requested ?limit/?offset window and returns a Pagination describing the full
// set. The list endpoints below fetch the whole collection from the agent (the
// apiserver returns it unpaginated over the single tunnel), so without this the
// handler returned the entire list while advertising a page size it never
// honoured — following "Next" (offset=20) re-ran the same unbounded fetch and
// returned the identical rows. Slicing server-side and reporting the real total
// makes HasMore / NextOffset accurate and stops the duplicate-page behaviour.
func pageWindow[T any](r *http.Request, items []T) ([]T, Pagination) {
	limit, offset := queryLimitOffset(r, 20)
	total := len(items)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return items[start:end], NewPagination(total, limit, offset, end-start)
}

func (h *WorkloadHandler) List(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := r.URL.Query().Get("namespace")
	kind := r.URL.Query().Get("kind")
	search := strings.ToLower(r.URL.Query().Get("search"))

	workloads, err := h.listWorkloads(r.Context(), clusterID, namespace, kind)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}

	all, names, err := h.authz.authorizedNamespaces(r.Context(), parseClusterUUID(clusterID), rbac.ResourceWorkloads, rbac.VerbList)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if !all {
		workloads = filterItemsByNamespaceKey(workloads, "namespace", names)
	}

	filtered := make([]map[string]any, 0, len(workloads))
	for _, item := range workloads {
		if search != "" && !strings.Contains(strings.ToLower(item["name"].(string)), search) && !strings.Contains(strings.ToLower(item["namespace"].(string)), search) {
			continue
		}
		filtered = append(filtered, item)
	}
	// Slice server-side to the requested page. RespondPaginated computes the
	// next/prev links from the same ?limit/?offset via queryInt, so paginate
	// (which reads them identically) keeps the returned page and the advertised
	// links in sync instead of returning the whole cluster on every page.
	RespondPaginated(w, r, paginate(filtered, r), int64(len(filtered)))
}

func (h *WorkloadHandler) Get(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	kind := chi.URLParam(r, "kind")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")

	resource, err := h.getWorkload(r.Context(), clusterID, kind, namespace, name)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, resource)
}

func (h *WorkloadHandler) Scale(w http.ResponseWriter, r *http.Request) {
	clusterID, kind, namespace, name := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	var req struct {
		Replicas int32 `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if _, err := scalePath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	op, err := h.enqueueOperation(withOperationIdempotency(r, "workloads"), "workload", workloadTargetKey(clusterID, kind, namespace, name), "scale", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		Replicas:  req.Replicas,
	}, currentUserUUID(r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue workload scale")
		return
	}
	h.recordWorkloadAudit(r, "workload.scale", kind, namespace, name, map[string]any{"clusterId": clusterID, "replicas": req.Replicas})
	RespondJSON(w, http.StatusAccepted, workloadOperationResponse(op))
}

func (h *WorkloadHandler) Restart(w http.ResponseWriter, r *http.Request) {
	clusterID, kind, namespace, name := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	if _, err := workloadPath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	op, err := h.enqueueOperation(withOperationIdempotency(r, "workloads"), "workload", workloadTargetKey(clusterID, kind, namespace, name), "restart", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	}, currentUserUUID(r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue workload restart")
		return
	}
	h.recordWorkloadAudit(r, "workload.restart", kind, namespace, name, map[string]any{"clusterId": clusterID})
	RespondJSON(w, http.StatusAccepted, workloadOperationResponse(op))
}

func (h *WorkloadHandler) Delete(w http.ResponseWriter, r *http.Request) {
	clusterID, kind, namespace, name := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
	if _, err := workloadPath(kind, namespace, name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKind, err.Error())
		return
	}
	op, err := h.enqueueOperation(withOperationIdempotency(r, "workloads"), "workload", workloadTargetKey(clusterID, kind, namespace, name), "delete", workloadOperationEnvelope{
		ClusterID: clusterID,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
	}, currentUserUUID(r))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue workload delete")
		return
	}
	h.recordWorkloadAudit(r, "workload.delete", kind, namespace, name, map[string]any{"clusterId": clusterID})
	RespondJSON(w, http.StatusAccepted, workloadOperationResponse(op))
}

func (h *WorkloadHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.WorkloadError, "workload store not configured")
		return
	}
	arg := sqlc.ListWorkloadOperationsParams{
		Limit:  int32(queryInt(r, "limit", 50)),
		Offset: int32(queryInt(r, "offset", 0)),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListWorkloadOperations(r.Context(), arg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.WorkloadError, "Failed to list workload operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	resp := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if restricted {
			clusterID, err := workloadOperationClusterID(op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
				continue
			}
		}
		resp = append(resp, workloadOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *WorkloadHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetWorkloadOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Workload operation not found")
		return
	}
	clusterID, err := workloadOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	resp := workloadOperationResponse(op)
	if events, err := h.queries.ListWorkloadOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = workloadOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *WorkloadHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetWorkloadOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Workload operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := workloadOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve workload operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceWorkloads, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueWorkloadOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RetryError, "Failed to retry workload operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "workload.operation.retry", "workload_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, workloadOperationResponse(requeued))
}

func (h *WorkloadHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{"reconciler": map[string]any{"enabled": false, "queueDepth": 0}})
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	// Fast path: unrestricted callers get exact, uncapped counts straight from
	// the database via a GROUP BY aggregation. This avoids streaming up to 1000
	// operation rows and recomputing per-operation authorization on every status
	// poll — the aggregation runs entirely server-side.
	if !restricted {
		rows, err := h.queries.CountWorkloadOperationsByStatus(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load workload controller status")
			return
		}
		counts := make(map[string]int, len(rows))
		staleRunning := 0
		for _, row := range rows {
			counts[row.Status] = int(row.Total)
			staleRunning += int(row.StaleRunning)
		}
		summary := operationStatusSummary{
			Counts:                counts,
			StaleRunning:          staleRunning,
			StaleThresholdSeconds: 60,
		}
		summary.QueueDepth = operationstate.QueueDepth(counts)
		RespondJSON(w, http.StatusOK, map[string]any{
			"reconciler": summary.reconcilerMap(),
			"operations": summary.Counts,
		})
		return
	}
	// Restricted callers need per-operation cluster authorization, which depends
	// on the operation payload; fall back to loading rows and filtering in Go.
	ops, err := h.queries.ListWorkloadOperations(r.Context(), sqlc.ListWorkloadOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load workload controller status")
		return
	}
	opSummary := summarizeOperations(r.Context(), ops, operationStatusSummaryConfig[sqlc.WorkloadOperation]{
		Status:    func(op sqlc.WorkloadOperation) string { return op.Status },
		CreatedAt: func(op sqlc.WorkloadOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.WorkloadOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.WorkloadOperation) bool {
			clusterID, err := workloadOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead)
		},
		Preview: func(_ context.Context, op sqlc.WorkloadOperation) map[string]any {
			return workloadOperationResponse(op)
		},
		StaleThresholdSeconds: 60,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"operations": opSummary.Counts,
	})
}

func workloadOperationClusterID(op sqlc.WorkloadOperation) (uuid.UUID, error) {
	var env workloadOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}

func (h *WorkloadHandler) ListNamespaces(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
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
	all, names, err := h.authz.authorizedNamespaces(r.Context(), parseClusterUUID(clusterID), rbac.ResourceClusters, rbac.VerbRead)
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
	RespondList(w, page, pagination)
}

func (h *WorkloadHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	nodes, err := h.getNodes(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	// Nodes come straight from the cluster's API unpaginated; slice to the
	// requested page so Total reflects the full set and Next advances correctly.
	page, pagination := pageWindow(r, nodes)
	RespondList(w, page, pagination)
}

func (h *WorkloadHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	nodeName := chi.URLParam(r, "node_name")
	detail, err := h.getNodeDetail(r.Context(), clusterID, nodeName)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, detail)
}

func (h *WorkloadHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	var events eventList
	path := "/api/v1/events"
	if limit := r.URL.Query().Get("limit"); limit != "" {
		path += "?limit=" + url.QueryEscape(limit)
	}
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
	all, names, err := h.authz.authorizedNamespaces(r.Context(), parseClusterUUID(clusterID), rbac.ResourceClusters, rbac.VerbRead)
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
	RespondList(w, page, pagination)
}

func (h *WorkloadHandler) ListPods(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := r.URL.Query().Get("namespace")
	pods, err := h.listPods(r.Context(), clusterID, namespace, "")
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	all, names, err := h.authz.authorizedNamespaces(r.Context(), parseClusterUUID(clusterID), rbac.ResourcePods, rbac.VerbList)
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
	RespondList(w, page, pagination)
}

func (h *WorkloadHandler) ListWorkloadPods(w http.ResponseWriter, r *http.Request) {
	clusterID, kind, namespace, name := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "kind"), chi.URLParam(r, "namespace"), chi.URLParam(r, "name")
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
	RespondList(w, page, pagination)
}

func (h *WorkloadHandler) DeletePod(w http.ResponseWriter, r *http.Request) {
	clusterID, namespace, pod := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "namespace"), chi.URLParam(r, "pod")
	resp, err := h.requester.Do(r.Context(), clusterID, http.MethodDelete, fmt.Sprintf("/api/v1/namespaces/%s/pods/%s", namespace, pod), nil, requestHeaders(""))
	if err != nil || ensureSuccess(resp) != nil {
		if err == nil {
			err = ensureSuccess(resp)
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *WorkloadHandler) PodLogs(w http.ResponseWriter, r *http.Request) {
	clusterID, namespace, pod := chi.URLParam(r, "cluster_id"), chi.URLParam(r, "namespace"), chi.URLParam(r, "pod")
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
		var wl workloadList
		if err := h.getJSON(ctx, clusterID, listPath, &wl); err != nil {
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
	if selector != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "labelSelector=" + url.QueryEscape(selector)
	}
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
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, headers)
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var out podMetadataList
	if err := parseJSONResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *WorkloadHandler) getNodes(ctx context.Context, clusterID string) ([]map[string]any, error) {
	var nodes nodeList
	if err := h.getJSON(ctx, clusterID, "/api/v1/nodes", &nodes); err != nil {
		return nil, err
	}
	var pods podList
	_ = h.getJSON(ctx, clusterID, "/api/v1/pods", &pods)
	podCounts := map[string]int{}
	for _, pod := range pods.Items {
		podCounts[pod.Spec.NodeName]++
	}
	items := make([]map[string]any, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		items = append(items, nodeSummaryMap(node, podCounts[node.Metadata.Name]))
	}
	return items, nil
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

func (h *WorkloadHandler) enqueueOperation(ctx context.Context, targetType, targetKey, operationType string, env workloadOperationEnvelope, userID pgtype.UUID) (sqlc.WorkloadOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.WorkloadOperation{}, err
	}
	params := sqlc.CreateWorkloadOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.WorkloadOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := h.queries.(interface {
			CreateWorkloadOperationIdempotent(context.Context, sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error)
		}); ok {
			op, err = creator.CreateWorkloadOperationIdempotent(ctx, sqlc.CreateWorkloadOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = h.queries.CreateWorkloadOperation(ctx, params)
	}
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func workloadOperationResponse(op sqlc.WorkloadOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func workloadOperationEventsResponse(events []sqlc.WorkloadOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *WorkloadHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside — one slow cluster must
	// not block other clusters' workload operations. Same shape as the
	// catalog/tools/monitoring reconcilers.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingWorkloadOperations(ctx))
}

func (h *WorkloadHandler) claimPendingWorkloadOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingWorkloadOperations(ctx, 20)
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.WorkloadOperation]{
		ID:        func(op sqlc.WorkloadOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.WorkloadOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.WorkloadOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.WorkloadOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.WorkloadOperation) {
			h.recordOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{"targetKey": op.TargetKey})
			_, _ = h.queries.MarkWorkloadOperationSuperseded(ctx, sqlc.MarkWorkloadOperationSupersededParams{ID: op.ID, ErrorMessage: operationSupersededMessage})
		},
		MarkRunning: func(ctx context.Context, op sqlc.WorkloadOperation) (sqlc.WorkloadOperation, error) {
			running, err := h.queries.MarkWorkloadOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.WorkloadOperation{}, err
			}
			h.recordOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{"operationType": running.OperationType, "targetKey": running.TargetKey})
			return running, nil
		},
		Claimed: func(running sqlc.WorkloadOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkWorkloadOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					_, _ = h.queries.MarkWorkloadOperationFailed(ctx, sqlc.MarkWorkloadOperationFailedParams{ID: running.ID, ErrorMessage: err.Error()})
				},
			}
		},
	})
}

func (h *WorkloadHandler) executeOperation(ctx context.Context, op sqlc.WorkloadOperation) error {
	var env workloadOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	switch op.OperationType {
	case "scale":
		payload, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": env.Replicas}})
		path, err := scalePath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "scale", "scaling workload", map[string]any{"replicas": env.Replicas})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodPatch, path, payload, requestHeaders("application/merge-patch+json"))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	case "restart":
		payload, _ := json.Marshal(map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"metadata": map[string]any{
						"annotations": map[string]string{
							"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
						},
					},
				},
			},
		})
		path, err := workloadPath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "restart", "restarting workload", map[string]any{})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodPatch, path, payload, requestHeaders("application/merge-patch+json"))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	case "delete":
		path, err := workloadPath(env.Kind, env.Namespace, env.Name)
		if err != nil {
			return err
		}
		h.recordOperationEvent(ctx, op.ID, "info", "delete", "deleting workload", map[string]any{})
		resp, err := h.requester.Do(ctx, env.ClusterID, http.MethodDelete, path, nil, requestHeaders(""))
		if err != nil {
			return err
		}
		return ensureSuccess(resp)
	default:
		return fmt.Errorf("unsupported workload operation type: %s", op.OperationType)
	}
}

func (h *WorkloadHandler) recordOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateWorkloadOperationEvent(ctx, sqlc.CreateWorkloadOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func workloadTargetKey(clusterID, kind, namespace, name string) string {
	return clusterID + ":" + kind + ":" + namespace + ":" + name
}

func (h *WorkloadHandler) recordWorkloadAudit(r *http.Request, action, kind, namespace, name string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	recordAudit(r, h.queries, action, "workload", kind+"/"+namespace+"/"+name, name, detail)
}

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
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	switch {
	case strings.HasSuffix(v, "m"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "m"), 64)
		return int(f)
	case strings.HasSuffix(v, "u"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "u"), 64)
		return int(f / 1000)
	case strings.HasSuffix(v, "n"):
		f, _ := strconv.ParseFloat(strings.TrimSuffix(v, "n"), 64)
		return int(f / 1000000)
	default:
		// Plain cores (may be float "0.5" or int "4").
		f, _ := strconv.ParseFloat(v, 64)
		return int(f * 1000)
	}
}

func parseMemory(v string) int {
	multipliers := map[string]int{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
	}
	for suffix, mult := range multipliers {
		if strings.HasSuffix(v, suffix) {
			n, _ := strconv.Atoi(strings.TrimSuffix(v, suffix))
			return n * mult
		}
	}
	n, _ := strconv.Atoi(v)
	return n
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
