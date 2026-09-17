package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

// note: cluster access errors use respondClusterAccessError (cluster_access_errors.go).

type WorkloadHandler struct {
	requester K8sRequester
	queries   WorkloadQuerier
	runTx     workloadRunTxFunc
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

type WorkloadMutationTx interface {
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
	CreateWorkloadOperation(context.Context, sqlc.CreateWorkloadOperationParams) (sqlc.WorkloadOperation, error)
	CreateWorkloadOperationIdempotent(context.Context, sqlc.CreateWorkloadOperationIdempotentParams) (sqlc.WorkloadOperation, error)
	RequeueWorkloadOperation(context.Context, uuid.UUID) (sqlc.WorkloadOperation, error)
}

type workloadRunTxFunc func(context.Context, func(WorkloadMutationTx) error) error

func (h *WorkloadHandler) SetRunTx(runTx workloadRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *WorkloadHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

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
	MarkWorkloadOperationCompleted(ctx context.Context, arg sqlc.MarkWorkloadOperationCompletedParams) (sqlc.WorkloadOperation, error)
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

func (h *WorkloadHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

// SetNamespaceScopedRBAC toggles per-namespace filtering of the list handlers
// (pods/namespaces/events/workloads). Wired from the
// namespace_scoped_rbac_enabled config flag. It defaults on so scoped callers
// receive only their authorized namespaces; disabling it requires an explicit
// operator override and restores cluster-level list authorization.
func (h *WorkloadHandler) SetNamespaceScopedRBAC(enabled bool) {
	if h == nil {
		return
	}
	h.authz.SetNamespaceScoped(enabled)
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
	go h.RunReconciler(ctx)
}

func (h *WorkloadHandler) RunReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	h.runReconciler(ctx)
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
