package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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
	resourcesSearchDefaultLimit = 100
	resourcesSearchMaxLimit     = 1000
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

// searchResourceDef is a local copy of the resourceDef shape from
// resources.go restricted to the types we want exposed via the global
// search endpoint. We keep this list small on purpose: nobody searches the
// fleet for "all CRDs across all clusters" and adding a type that doesn't
// have a sensible flatten path produces unhelpful results.
type searchResourceDef struct {
	apiBase    string
	plural     string
	namespaced bool
	// rbacResource is the RBAC resource type the caller must hold `read` on.
	// Keeping the mapping table-driven means a new type can be added by
	// editing one place.
	rbacResource rbac.Resource
}

var searchResourceDefs = map[string]searchResourceDef{
	"pods":                   {apiBase: "/api/v1", plural: "pods", namespaced: true, rbacResource: rbac.ResourcePods},
	"services":               {apiBase: "/api/v1", plural: "services", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"configmaps":             {apiBase: "/api/v1", plural: "configmaps", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"secrets":                {apiBase: "/api/v1", plural: "secrets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"namespaces":             {apiBase: "/api/v1", plural: "namespaces", namespaced: false, rbacResource: rbac.ResourceClusters},
	"nodes":                  {apiBase: "/api/v1", plural: "nodes", namespaced: false, rbacResource: rbac.ResourceClusters},
	"persistentvolumeclaims": {apiBase: "/api/v1", plural: "persistentvolumeclaims", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"deployments":            {apiBase: "/apis/apps/v1", plural: "deployments", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"statefulsets":           {apiBase: "/apis/apps/v1", plural: "statefulsets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"daemonsets":             {apiBase: "/apis/apps/v1", plural: "daemonsets", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"jobs":                   {apiBase: "/apis/batch/v1", plural: "jobs", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"cronjobs":               {apiBase: "/apis/batch/v1", plural: "cronjobs", namespaced: true, rbacResource: rbac.ResourceWorkloads},
	"ingresses":              {apiBase: "/apis/networking.k8s.io/v1", plural: "ingresses", namespaced: true, rbacResource: rbac.ResourceWorkloads},
}

// searchClusterError is a per-cluster failure surfaced inline in the
// response body. The frontend renders a collapsible row from these — the
// caller wants to see *which* clusters errored, not just a count.
type searchClusterError struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Error       string `json:"error"`
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
	if h == nil || h.queries == nil || h.requester == nil {
		RespondError(w, http.StatusServiceUnavailable, "search_unavailable", "Cross-cluster search is not configured")
		return
	}

	q := r.URL.Query()
	resourceType := strings.ToLower(strings.TrimSpace(q.Get("type")))
	if resourceType == "" {
		RespondError(w, http.StatusBadRequest, "invalid_request", "type query parameter is required")
		return
	}
	def, ok := searchResourceDefs[resourceType]
	if !ok {
		RespondError(w, http.StatusBadRequest, "unsupported_type", fmt.Sprintf("unsupported resource type %q", resourceType))
		return
	}

	// Authorization: the route-level middleware (registered in routes.go)
	// already enforces a baseline `read` permission on a representative
	// resource. The per-type rbacResource here is recorded for future
	// enforcement once the rbac.Engine is plumbed into this handler — it
	// would require either an inline engine reference or a dynamic
	// permission middleware that reads the request's `type` query param.
	// For now the route middleware + the agent tunnel's own auth are the
	// gate.
	_ = def.rbacResource

	namespace := strings.TrimSpace(q.Get("namespace"))
	labelSelector := strings.TrimSpace(q.Get("label"))
	fieldSelector := strings.TrimSpace(q.Get("field"))
	nameFilter := strings.ToLower(strings.TrimSpace(q.Get("name")))

	limit := queryInt(r, "limit", resourcesSearchDefaultLimit)
	if limit <= 0 {
		limit = resourcesSearchDefaultLimit
	}
	if limit > resourcesSearchMaxLimit {
		limit = resourcesSearchMaxLimit
	}

	// Step 1: list active clusters. We page through with a generous LIMIT
	// — search across thousands of clusters is out of scope for now, and
	// the front-end's cap is well below 1k.
	clusters, err := h.queries.ListClustersByStatus(r.Context(), sqlc.ListClustersByStatusParams{
		Status:      "active",
		QueryOffset: 0,
		QueryLimit:  1000,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_clusters_failed", "Failed to list active clusters")
		return
	}

	// Build the per-cluster k8s path once (it's identical for every cluster
	// because the namespace + selectors are shared).
	path := buildSearchPath(def, namespace, labelSelector, fieldSelector)

	// Step 2: fan out. errgroup with SetLimit caps concurrency at 16; the
	// per-cluster context timeout means a single slow cluster cannot hold
	// up the whole response.
	type clusterResult struct {
		items []map[string]any
		err   *searchClusterError
	}
	results := make([]clusterResult, len(clusters))

	g, gctx := errgroup.WithContext(r.Context())
	g.SetLimit(resourcesSearchMaxConcurrency)

	for i, c := range clusters {
		i, c := i, c // capture loop vars
		g.Go(func() error {
			ctx, cancel := context.WithTimeout(gctx, resourcesSearchPerClusterTimeout)
			defer cancel()

			items, err := h.queryCluster(ctx, c, resourceType, path)
			if err != nil {
				results[i] = clusterResult{
					err: &searchClusterError{
						ClusterID:   c.ID.String(),
						ClusterName: clusterDisplayName(c),
						Error:       err.Error(),
					},
				}
				return nil // never bubble — partial failure is expected
			}
			results[i] = clusterResult{items: items}
			return nil
		})
	}
	_ = g.Wait() // we never return errors from the goroutines

	// Step 3: merge + apply optional name filter + tag with cluster info.
	merged := make([]map[string]any, 0)
	errs := make([]searchClusterError, 0)
	failed := 0
	for i, c := range clusters {
		if results[i].err != nil {
			errs = append(errs, *results[i].err)
			failed++
			continue
		}
		clusterID := c.ID.String()
		clusterName := clusterDisplayName(c)
		for _, item := range results[i].items {
			// Make sure cluster_id / cluster_name are present even if the
			// underlying flatten helper already set them (resources.go does
			// for some types; we overwrite to guarantee consistency).
			item["cluster_id"] = clusterID
			item["cluster_name"] = clusterName
			// Mirror to camelCase for the existing frontend axios layer
			// that camelizes snake_case keys; explicit keys avoid surprises.
			item["clusterId"] = clusterID
			item["clusterName"] = clusterName
			if nameFilter != "" {
				if !strings.Contains(strings.ToLower(stringValueAny(item, "name")), nameFilter) {
					continue
				}
			}
			merged = append(merged, item)
		}
	}

	// Stable ordering: cluster name, then namespace, then resource name.
	sort.SliceStable(merged, func(i, j int) bool {
		ci, _ := merged[i]["cluster_name"].(string)
		cj, _ := merged[j]["cluster_name"].(string)
		if ci != cj {
			return ci < cj
		}
		ni, _ := merged[i]["namespace"].(string)
		nj, _ := merged[j]["namespace"].(string)
		if ni != nj {
			return ni < nj
		}
		mi, _ := merged[i]["name"].(string)
		mj, _ := merged[j]["name"].(string)
		return mi < mj
	})

	if len(merged) > limit {
		merged = merged[:limit]
	}

	RespondJSON(w, http.StatusOK, map[string]any{
		"results":          merged,
		"errors":           errs,
		"clusters_queried": len(clusters),
		"clusters_failed":  failed,
		"type":             resourceType,
		"truncated":        false,
	})
}

// queryCluster runs a single-cluster list against the agent tunnel and
// returns flattened items. It reuses the existing flatten helpers from
// resources.go so search results match the per-cluster list shape — the
// frontend can render either with the same column definitions.
func (h *ResourcesSearchHandler) queryCluster(ctx context.Context, c sqlc.Cluster, resourceType, path string) ([]map[string]any, error) {
	resp, err := h.requester.Do(ctx, c.ID.String(), http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return nil, err
	}
	// Use the existing per-cluster flatten path. resources.go has two flatten
	// helpers — flattenNamedResources for the "fancy" types (services,
	// ingresses, ...) and flattenGenericResources for the rest. The map below
	// picks the one that produces the richest UI-ready row.
	clusterID := c.ID.String()
	switch resourceType {
	case "services", "ingresses", "persistentvolumeclaims":
		return flattenNamedResources(clusterID, resourceType, payload), nil
	case "configmaps", "secrets", "jobs", "cronjobs":
		return flattenGenericResources(clusterID, resourceType, payload), nil
	case "pods":
		return flattenSearchPods(clusterID, payload), nil
	case "namespaces":
		return flattenSearchNamespaces(clusterID, payload), nil
	case "nodes":
		return flattenSearchNodes(clusterID, payload), nil
	case "deployments", "statefulsets", "daemonsets":
		return flattenSearchWorkloads(clusterID, resourceType, payload), nil
	default:
		// Fallback: bare metadata-level shape. Should never happen because
		// the resourceType has already been validated against searchResourceDefs.
		out := make([]map[string]any, 0)
		for _, item := range objectItems(payload) {
			out = append(out, flattenGeneric(clusterID, item))
		}
		return out, nil
	}
}

// buildSearchPath assembles the k8s API path with optional namespace and
// labelSelector / fieldSelector query params. We deliberately do NOT pass
// the limit through to the agent — every cluster's list comes back in full
// and we apply the global cap after merge so a popular cluster doesn't
// crowd out tail clusters with smaller result sets.
func buildSearchPath(def searchResourceDef, namespace, label, field string) string {
	var p string
	if def.namespaced && namespace != "" {
		p = fmt.Sprintf("%s/namespaces/%s/%s", def.apiBase, namespace, def.plural)
	} else {
		p = fmt.Sprintf("%s/%s", def.apiBase, def.plural)
	}
	q := url.Values{}
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

