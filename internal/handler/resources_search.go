package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// resourcesSearchMaxConcurrency caps how many clusters we hit in parallel for
// a single fan-out search. Most fleets have far more than 16 clusters but the
// agent tunnel multiplexes streams over a single websocket per cluster — so
// the bottleneck is the *server* side of the tunnel, not the network. 16 is
// a conservative upper bound that keeps p99 latency reasonable on large
// fleets without saturating the tunnel goroutine pool. The plan suggests
// "e.g. 16 concurrent" and that's what we honor.
const resourcesSearchMaxConcurrency = 16

// resourcesSearchPerClusterTimeout bounds how long any single cluster gets
// before we give up on it and surface a partial-failure entry to the client.
// Slow clusters must not block the whole response — the UI already renders
// `clusters_failed` collapsibly.
const resourcesSearchPerClusterTimeout = 5 * time.Second

// resourcesSearchDefaultLimit / Cap mirror Kubernetes-style list pagination
// hints. The cap protects against runaway responses when somebody passes
// limit=10000000.
const (
	resourcesSearchDefaultLimit  = 100
	resourcesSearchMaxLimit      = 1000
	resourcesSearchFleetPageSize = 250
	resourcesSearchMaxErrors     = 100
	resourcesSearchTimeout       = 15 * time.Second
)

// ResourcesSearchHandler fans a single resource-list query out across every
// active cluster. It deliberately does NOT take ownership of resourceDefs (the
// existing per-cluster handler in resources.go already maps types → k8s API
// paths) — instead it has a small dedicated mapping for the cross-cluster
// search surface, which intentionally only includes the types we want users
// to be able to search globally. Cluster-scoped resources (nodes, namespaces,
// PVs, storage classes, ...) are listed across the whole fleet without a
// namespace filter; namespaced ones honor the optional namespace query param.
type ResourcesSearchHandler struct {
	requester K8sRequester
	queries   ResourcesSearchQuerier
	authz     authorizationSupport
	audit     any
	// Timeouts are overrideable by package tests; production uses the bounded
	// defaults above.
	requestTimeout    time.Duration
	perClusterTimeout time.Duration
}

// ResourcesSearchQuerier is the minimal slice of sqlc.Queries the search
// handler needs. Defining a narrow interface keeps unit testing trivial and
// avoids any dependency on the full ClusterQuerier interface.
type ResourcesSearchQuerier interface {
	ListClustersByStatus(ctx context.Context, arg sqlc.ListClustersByStatusParams) ([]sqlc.Cluster, error)
}

// NewResourcesSearchHandler constructs a search handler. Both deps are
// nil-safe: the handler returns 503 when either is missing so unit tests and
// partially-wired servers degrade rather than panic.
func NewResourcesSearchHandler(queries ResourcesSearchQuerier, requester K8sRequester) *ResourcesSearchHandler {
	return &ResourcesSearchHandler{queries: queries, requester: requester}
}

func (h *ResourcesSearchHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *ResourcesSearchHandler) SetAuditWriter(audit any) {
	h.audit = audit
}

// searchResourceDef is a local copy of the resourceDef shape from
// resources.go restricted to the types we want exposed via the global
// search endpoint. We keep this list small on purpose: nobody searches the
// fleet for "all CRDs across all clusters" and adding a type that doesn't
// have a sensible flatten path produces unhelpful results.
type searchResourceDef struct {
	apiBase    string
	plural     string
	namespaced bool
	// rbacResource is the RBAC resource type the caller must hold `list` on
	// for a given cluster in order to receive rows from that cluster. Keeping
	// the mapping table-driven means a new type can be added by editing one
	// place. The categorization choices below intentionally fold several
	// related Kubernetes kinds onto a smaller set of RBAC resources from
	// internal/rbac/types.go:
	//   - pods, events                  -> rbac.ResourcePods
	//   - deployments, statefulsets,
	//     daemonsets, replicasets,
	//     jobs, cronjobs                -> rbac.ResourceWorkloads
	//   - services, endpoints           -> rbac.ResourceServices
	//   - ingresses, gateway-api        -> rbac.ResourceIngresses
	//   - networkpolicies              -> rbac.ResourceNetworkPolicies
	//   - secrets                       -> rbac.ResourceSecrets
	//   - configmaps                    -> rbac.ResourceConfigMaps
	//   - persistentvolumes,
	//     persistentvolumeclaims,
	//     storageclasses                -> rbac.ResourceStorage
	//   - nodes                         -> rbac.ResourceNodes
	//   - namespaces                    -> rbac.ResourceClusters
	//   - unknown / default             -> rbac.ResourceWorkloads
	rbacResource rbac.Resource
}

var searchResourceDefs = map[string]searchResourceDef{
	"pods":                   {apiBase: "/api/v1", plural: "pods", namespaced: true, rbacResource: rbac.ResourcePods},
	"events":                 {apiBase: "/api/v1", plural: "events", namespaced: true, rbacResource: rbac.ResourcePods},
	"endpoints":              {apiBase: "/api/v1", plural: "endpoints", namespaced: true, rbacResource: rbac.ResourceServices},
	"services":               {apiBase: "/api/v1", plural: "services", namespaced: true, rbacResource: rbac.ResourceServices},
	"configmaps":             {apiBase: "/api/v1", plural: "configmaps", namespaced: true, rbacResource: rbac.ResourceConfigMaps},
	"secrets":                {apiBase: "/api/v1", plural: "secrets", namespaced: true, rbacResource: rbac.ResourceSecrets},
	"namespaces":             {apiBase: "/api/v1", plural: "namespaces", namespaced: false, rbacResource: rbac.ResourceClusters},
	"nodes":                  {apiBase: "/api/v1", plural: "nodes", namespaced: false, rbacResource: rbac.ResourceNodes},
	"persistentvolumes":      {apiBase: "/api/v1", plural: "persistentvolumes", namespaced: false, rbacResource: rbac.ResourceStorage},
	"persistentvolumeclaims": {apiBase: "/api/v1", plural: "persistentvolumeclaims", namespaced: true, rbacResource: rbac.ResourceStorage},
	"storageclasses":         {apiBase: "/apis/storage.k8s.io/v1", plural: "storageclasses", namespaced: false, rbacResource: rbac.ResourceStorage},
	"deployments":            {apiBase: "/apis/apps/v1", plural: "deployments", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"statefulsets":           {apiBase: "/apis/apps/v1", plural: "statefulsets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"daemonsets":             {apiBase: "/apis/apps/v1", plural: "daemonsets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"replicasets":            {apiBase: "/apis/apps/v1", plural: "replicasets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"jobs":                   {apiBase: "/apis/batch/v1", plural: "jobs", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"cronjobs":               {apiBase: "/apis/batch/v1", plural: "cronjobs", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"ingresses":              {apiBase: "/apis/networking.k8s.io/v1", plural: "ingresses", namespaced: true, rbacResource: rbac.ResourceIngresses},
	"networkpolicies":        {apiBase: "/apis/networking.k8s.io/v1", plural: "networkpolicies", namespaced: true, rbacResource: rbac.ResourceNetworkPolicies},
	"gateways":               {apiBase: "/apis/gateway.networking.k8s.io/v1", plural: "gateways", namespaced: true, rbacResource: rbac.ResourceIngresses},
	"httproutes":             {apiBase: "/apis/gateway.networking.k8s.io/v1", plural: "httproutes", namespaced: true, rbacResource: rbac.ResourceIngresses},
}

// rbacResourceForType maps a search `type` query parameter to the
// rbac.Resource the caller must hold `list` on. Unknown / unsupported types
// fall through to rbac.ResourceWorkloads, matching the default categorization
// documented on searchResourceDef.rbacResource above. This helper exists in
// addition to searchResourceDefs so that resource-type → RBAC resource lookup
// works even on paths where the underlying API path mapping is not needed
// (and is a single, easily-grepped definition of the policy).
func rbacResourceForType(resourceType string) rbac.Resource {
	if def, ok := searchResourceDefs[strings.ToLower(strings.TrimSpace(resourceType))]; ok {
		return def.rbacResource
	}
	return rbac.ResourceWorkloads
}

// searchClusterError is a per-cluster failure surfaced inline in the
// response body. The frontend renders a collapsible row from these — the
// caller wants to see *which* clusters errored, not just a count.
type searchClusterError struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Error       string `json:"error"`
}

type searchClusterResult struct {
	cluster   sqlc.Cluster
	items     []map[string]any
	err       error
	truncated bool
}

// Search handles GET /api/v1/resources/search/.
//
// Query parameters:
//   - type      (required) one of the keys in searchResourceDefs
//   - namespace (optional) filter to a single namespace; ignored for non-namespaced types
//   - label     (optional) label selector, passed verbatim to k8s
//   - field     (optional) field selector, passed verbatim to k8s
//   - name      (optional) substring filter applied client-side after merge
//   - limit     (default 100, cap 1000) max items returned across all clusters
//
// Response shape:
//
//	{ "data": {
//	    "results": [...],
//	    "errors":  [{"cluster_id","cluster_name","error"}, ...],
//	    "clusters_queried": N,
//	    "clusters_failed":  M
//	} }
func (h *ResourcesSearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	if h == nil || h.queries == nil || h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.SearchUnavailable, "Cross-cluster search is not configured")
		return
	}

	q := r.URL.Query()
	resourceType := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if resourceType == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "type query parameter is required")
		return
	}
	def, ok := searchResourceDefs[resourceType]
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.UnsupportedType, fmt.Sprintf("unsupported resource type %q", resourceType))
		return
	}

	namespace := strings.TrimSpace(q.Get("namespace"))
	labelSelector := strings.TrimSpace(q.Get("label"))
	fieldSelector := strings.TrimSpace(q.Get("field"))
	nameFilter := strings.ToLower(strings.TrimSpace(q.Get("name")))

	limit := queryLimit(r, resourcesSearchDefaultLimit)
	if limit <= 0 {
		limit = resourcesSearchDefaultLimit
	}
	if limit > resourcesSearchMaxLimit {
		limit = resourcesSearchMaxLimit
	}
	requestTimeout := h.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = resourcesSearchTimeout
	}
	searchCtx, cancelSearch := context.WithTimeout(r.Context(), requestTimeout)
	defer cancelSearch()

	// Step 1: page the complete active fleet. A fixed first-page cap silently
	// hid clusters at scale and made authorization/results depend on creation
	// order.
	clusters, err := h.listActiveClusters(searchCtx)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListClustersFailed, "Failed to list active clusters")
		return
	}
	clusters, authErr := h.authorizedSearchClusters(searchCtx, clusters, def.rbacResource)
	if authErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	if len(clusters) == 0 {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "You do not have permission to search this resource type")
		return
	}

	// Build the per-cluster k8s path once (it's identical for every cluster
	// because the namespace + selectors are shared).
	path := buildSearchPath(def, namespace, labelSelector, fieldSelector, limit)
	if resourceType == "secrets" {
		recordAudit(r, h.audit, "cluster.secret.read", "cluster", "*", "secrets", map[string]any{
			"scope":               "cross_cluster_search",
			"resource_type":       resourceType,
			"k8s_path":            path,
			"namespace":           namespace,
			"label_selector":      labelSelector,
			"field_selector":      fieldSelector,
			"name_filter":         nameFilter,
			"limit":               limit,
			"clusters_authorized": len(clusters),
		})
	}

	// Step 2: bounded worker fan-out. Results stream directly into the global
	// top-K heap below instead of retaining one response slice per cluster.
	// The fleet deadline bounds total latency independently of fleet size;
	// every started cluster also gets a tighter timeout.
	resultStream := h.searchClusters(searchCtx, clusters, resourceType, path, nameFilter, limit)

	// Step 3: stream-merge with a bounded top-K max-heap. Each Kubernetes
	// request receives the global limit as a server-side list bound, and this
	// heap keeps only the globally smallest K rows.
	topK := newSearchTopKHeap(limit)
	errs := make([]searchClusterError, 0)
	omittedErrors := 0
	failed, attempted, matches := 0, 0, 0
	truncated := false
	for result := range resultStream {
		attempted++
		if result.err != nil {
			failed++
			if !appendSearchError(&errs, searchClusterError{
				ClusterID:   result.cluster.ID.String(),
				ClusterName: clusterDisplayName(result.cluster),
				Error:       result.err.Error(),
			}) {
				omittedErrors++
			}
			continue
		}
		truncated = truncated || result.truncated
		clusterID := result.cluster.ID.String()
		clusterName := clusterDisplayName(result.cluster)
		for _, item := range result.items {
			// Make sure cluster_id / cluster_name are present even if the
			// underlying flatten helper already set them (resources.go does
			// for some types; we overwrite to guarantee consistency).
			item["cluster_id"] = clusterID
			item["cluster_name"] = clusterName
			// Mirror to camelCase for the existing frontend axios layer
			// that camelizes snake_case keys; explicit keys avoid surprises.
			item["clusterId"] = clusterID
			item["clusterName"] = clusterName
			matches++
			topK.push(item)
		}
	}
	// The dispatcher stops feeding new work at the fleet deadline. Account for
	// clusters that never started without manufacturing an unbounded error
	// array; clusters_failed remains exact and the final aggregate error makes
	// the omission explicit.
	skipped := len(clusters) - attempted
	if skipped > 0 {
		failed += skipped
		if !appendSearchError(&errs, searchClusterError{
			ClusterID:   "*",
			ClusterName: "remaining clusters",
			Error:       fmt.Sprintf("fleet search deadline reached before %d cluster queries started", skipped),
		}) {
			omittedErrors += skipped
		}
	}
	if omittedErrors > 0 {
		errs[len(errs)-1] = searchClusterError{
			ClusterID:   "*",
			ClusterName: "additional failures",
			Error:       fmt.Sprintf("%d additional cluster failures omitted", omittedErrors+1),
		}
	}
	truncated = truncated || failed > 0 || matches > limit
	sort.Slice(errs, func(i, j int) bool {
		if errs[i].ClusterName == errs[j].ClusterName {
			return errs[i].ClusterID < errs[j].ClusterID
		}
		return errs[i].ClusterName < errs[j].ClusterName
	})
	merged := topK.sorted()
	outcome := "complete"
	if truncated {
		outcome = "partial"
	}
	observability.RecordResourceSearch(outcome, attempted, failed, startedAt)

	RespondJSON(w, http.StatusOK, map[string]any{
		"results":          merged,
		"errors":           errs,
		"clusters_queried": attempted,
		"clusters_failed":  failed,
		"type":             resourceType,
		"truncated":        truncated,
	})
}

func (h *ResourcesSearchHandler) listActiveClusters(ctx context.Context) ([]sqlc.Cluster, error) {
	clusters := make([]sqlc.Cluster, 0, resourcesSearchFleetPageSize)
	for offset := int32(0); ; offset += resourcesSearchFleetPageSize {
		page, err := h.queries.ListClustersByStatus(ctx, sqlc.ListClustersByStatusParams{
			Status:      "active",
			QueryOffset: offset,
			QueryLimit:  resourcesSearchFleetPageSize,
		})
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, page...)
		if len(page) < int(resourcesSearchFleetPageSize) {
			return clusters, nil
		}
	}
}

// searchClusters fans out through a fixed worker pool and streams completed
// responses back to the caller. Once the fleet context expires, the producer
// stops scheduling clusters that have not started; in-flight request contexts
// are cancelled by the same deadline.
func (h *ResourcesSearchHandler) searchClusters(
	ctx context.Context,
	clusters []sqlc.Cluster,
	resourceType, path, nameFilter string,
	limit int,
) <-chan searchClusterResult {
	results := make(chan searchClusterResult)
	jobs := make(chan sqlc.Cluster)
	workerCount := min(resourcesSearchMaxConcurrency, len(clusters))
	perClusterTimeout := h.clusterTimeout()

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for cluster := range jobs {
				clusterCtx, cancel := context.WithTimeout(ctx, perClusterTimeout)
				items, truncated, err := h.queryCluster(clusterCtx, cluster, resourceType, path, nameFilter, limit)
				cancel()
				results <- searchClusterResult{cluster: cluster, items: items, err: err, truncated: truncated}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, cluster := range clusters {
			select {
			case jobs <- cluster:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	return results
}

func (h *ResourcesSearchHandler) clusterTimeout() time.Duration {
	if h.perClusterTimeout > 0 {
		return h.perClusterTimeout
	}
	return resourcesSearchPerClusterTimeout
}

func appendSearchError(errors *[]searchClusterError, entry searchClusterError) bool {
	if len(*errors) >= resourcesSearchMaxErrors {
		return false
	}
	*errors = append(*errors, entry)
	return true
}

// authorizedSearchClusters narrows the candidate cluster set to those where
// the caller holds `<resource>:list` for the requested search type. This is
// the per-result, per-resource-type filter that replaces the coarse,
// route-level RBAC gate the search handler used to rely on. Filtering here
// (a) avoids leaking the existence of clusters the caller cannot see and
// (b) reduces tunnel load by not even fanning out to clusters whose results
// would be discarded.
func (h *ResourcesSearchHandler) authorizedSearchClusters(ctx context.Context, clusters []sqlc.Cluster, resource rbac.Resource) ([]sqlc.Cluster, error) {
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	if !restricted {
		return clusters, nil
	}
	filtered := make([]sqlc.Cluster, 0, len(clusters))
	for _, c := range clusters {
		if h.authz.allowsCluster(bindings, c.ID, resource, rbac.VerbList) {
			filtered = append(filtered, c)
		}
	}
	return filtered, nil
}

// queryCluster runs a single-cluster list against the agent tunnel and
// returns flattened items. It reuses the existing flatten helpers from
// resources.go so search results match the per-cluster list shape — the
// frontend can render either with the same column definitions.
func (h *ResourcesSearchHandler) queryCluster(
	ctx context.Context,
	c sqlc.Cluster,
	resourceType, path, nameFilter string,
	limit int,
) ([]map[string]any, bool, error) {
	resp, err := h.requester.Do(ctx, c.ID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, false, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, false, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return nil, false, err
	}
	// Use the existing per-cluster flatten path. resources.go has two flatten
	// helpers — flattenNamedResources for the "fancy" types (services,
	// ingresses, ...) and flattenGenericResources for the rest. The map below
	// picks the one that produces the richest UI-ready row.
	clusterID := c.ID.String()
	var items []map[string]any
	switch resourceType {
	case "services", "ingresses", "persistentvolumeclaims":
		items = flattenNamedResources(clusterID, resourceType, payload)
	case "configmaps", "secrets", "jobs", "cronjobs":
		items = flattenGenericResources(clusterID, resourceType, payload)
	case "pods":
		items = flattenSearchPods(clusterID, payload)
	case "namespaces":
		items = flattenSearchNamespaces(clusterID, payload)
	case "nodes":
		items = flattenSearchNodes(clusterID, payload)
	case "deployments", "statefulsets", "daemonsets":
		items = flattenSearchWorkloads(clusterID, resourceType, payload)
	default:
		// Fallback: bare metadata-level shape. Used for the long tail of
		// types in searchResourceDefs that don't have a richer flatten
		// helper (events, endpoints, replicasets, networkpolicies,
		// gateway-api kinds, persistent volumes, storage classes, ...).
		// The frontend renders these with the generic name/namespace columns.
		items = make([]map[string]any, 0)
		for _, item := range objectItems(payload) {
			items = append(items, flattenGeneric(clusterID, item))
		}
	}

	filtered := items[:0]
	for _, item := range items {
		if nameFilter != "" && !strings.Contains(strings.ToLower(stringValueAny(item, "name")), nameFilter) {
			continue
		}
		filtered = append(filtered, item)
	}
	truncated := kubernetesListHasMore(payload)
	if len(filtered) > limit {
		filtered = filtered[:limit]
		truncated = true
	}
	return filtered, truncated, nil
}

// buildSearchPath assembles the k8s API path with optional namespace and
// labelSelector / fieldSelector query params. The global result cap is also
// pushed into every Kubernetes List call, bounding each tunnel response.
func buildSearchPath(def searchResourceDef, namespace, label, field string, limit int) string {
	var p string
	if def.namespaced && namespace != "" {
		p = fmt.Sprintf("%s/namespaces/%s/%s", def.apiBase, url.PathEscape(namespace), def.plural)
	} else {
		p = fmt.Sprintf("%s/%s", def.apiBase, def.plural)
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprint(limit))
	if label != "" {
		q.Set("labelSelector", label)
	}
	if field != "" {
		q.Set("fieldSelector", field)
	}
	if enc := q.Encode(); enc != "" {
		p += "?" + enc
	}
	return p
}

func kubernetesListHasMore(payload map[string]any) bool {
	metadata, _ := payload["metadata"].(map[string]any)
	continuation, _ := metadata["continue"].(string)
	return continuation != ""
}

// flattenSearchPods produces a UI-ready row for each pod. Mirrors podToMap
// from workloads.go (which we deliberately can't call because that handler
// owns its own private types) but trimmed to the columns the global search
// table renders.
func flattenSearchPods(clusterID string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		entry["status"] = stringValue(item, "status", "phase")
		entry["phase"] = stringValue(item, "status", "phase")
		entry["node"] = stringValue(item, "spec", "nodeName")
		entry["ip"] = stringValue(item, "status", "podIP")
		entry["age"] = humanAgeFromString(stringValue(item, "metadata", "creationTimestamp"))
		entry["type"] = "Pod"
		// readyCount and total
		ready, total := podReadyCount(item)
		entry["ready"] = fmt.Sprintf("%d/%d", ready, total)
		out = append(out, entry)
	}
	return out
}

// podReadyCount mirrors the readyCount/total computation from podToMap.
func podReadyCount(item map[string]any) (int, int) {
	containers, _ := nestedSlice(item, "spec", "containers")
	statuses, _ := nestedSlice(item, "status", "containerStatuses")
	ready := 0
	for _, raw := range statuses {
		s, _ := raw.(map[string]any)
		if boolValue(s, "ready") {
			ready++
		}
	}
	return ready, len(containers)
}

func flattenSearchNamespaces(clusterID string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		entry["status"] = stringValue(item, "status", "phase")
		entry["age"] = humanAgeFromString(stringValue(item, "metadata", "creationTimestamp"))
		entry["type"] = "Namespace"
		out = append(out, entry)
	}
	return out
}

func flattenSearchNodes(clusterID string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		entry["status"] = nodeReadyStatus(item)
		entry["age"] = humanAgeFromString(stringValue(item, "metadata", "creationTimestamp"))
		entry["type"] = "Node"
		out = append(out, entry)
	}
	return out
}

// nodeReadyStatus walks status.conditions for the Ready condition and
// returns "Ready" / "NotReady". Mirrors nodeStatus in workloads.go.
func nodeReadyStatus(item map[string]any) string {
	conds, _ := nestedSlice(item, "status", "conditions")
	for _, raw := range conds {
		c, _ := raw.(map[string]any)
		if stringValueMap(c, "type") == "Ready" {
			if stringValueMap(c, "status") == "True" {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func flattenSearchWorkloads(clusterID, resourceType string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		desired := intValue(item, "spec", "replicas")
		ready := intValue(item, "status", "readyReplicas")
		// daemonsets surface desired/ready under different keys
		if resourceType == "daemonsets" {
			desired = intValue(item, "status", "desiredNumberScheduled")
			ready = intValue(item, "status", "numberReady")
		}
		entry["replicas"] = desired
		entry["ready"] = fmt.Sprintf("%d/%d", ready, desired)
		entry["status"] = workloadStatusFromMap(ready, desired)
		entry["age"] = humanAgeFromString(stringValue(item, "metadata", "creationTimestamp"))
		entry["type"] = strings.Title(strings.TrimSuffix(resourceType, "s")) //nolint:staticcheck // Title is fine for ASCII resource names
		out = append(out, entry)
	}
	return out
}

func workloadStatusFromMap(ready, desired int) string {
	if desired == 0 {
		return "Unknown"
	}
	if ready >= desired {
		return "Healthy"
	}
	return "Degraded"
}

// humanAgeFromString accepts an RFC3339 timestamp string and returns a
// short human-readable age like "2d", "5h", "30m". Falls back to "" when
// the input is empty or unparseable.
func humanAgeFromString(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// kubernetes sometimes uses time without timezone offset
		if t2, err2 := time.Parse("2006-01-02T15:04:05Z", ts); err2 == nil {
			t = t2
		} else {
			return ""
		}
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// clusterDisplayName picks the friendliest label for a cluster row. The
// frontend prefers display_name; falling back to name keeps the response
// non-empty when a cluster was registered without a display name.
func clusterDisplayName(c sqlc.Cluster) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}

// stringValueAny tolerates either string or non-string values when reading
// a top-level key from the flattened item map. The flatten helpers already
// emit strings for "name", but this guards against future shape drift.
func stringValueAny(item map[string]any, key string) string {
	if item == nil {
		return ""
	}
	v, ok := item[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
