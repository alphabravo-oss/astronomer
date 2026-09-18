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

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

const (
	maxResourceCountTypes       = 16
	resourceCountConcurrency    = 8
	resourceCountRequestTimeout = 12 * time.Second
)

// PartialObjectMetadataList asks the apiserver to omit spec, status and Secret
// data. There is deliberately no application/json fallback: a server that
// cannot honor metadata-only list negotiation is reported as unavailable
// instead of pulling sensitive object bodies into the management plane.
const metadataListAccept = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"

type resourceCountsResponse struct {
	Counts      map[string]int64  `json:"counts"`
	Unavailable map[string]string `json:"unavailable,omitempty"`
}

type metadataCountList struct {
	Metadata struct {
		Continue           string `json:"continue"`
		RemainingItemCount *int64 `json:"remainingItemCount"`
	} `json:"metadata"`
	Items []struct{} `json:"items"`
}

type resourceCountJob struct {
	ResourceType string
	Path         string
}

type resourceCountResult struct {
	ResourceType string
	Count        int64
	Err          error
}

// CountResources returns counts for an explicit, bounded set of Kubernetes
// resource types. Authorization is evaluated independently per type. A
// namespace-confined caller is queried only inside their allowed namespaces;
// an unauthorized type is omitted rather than leaking its count.
func (h *ResourceHandler) CountResources(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	resourceTypes, err := parseResourceCountTypes(r.URL.Query().Get("resources"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "tunnel requester not configured")
		return
	}

	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	requestedNamespaces := canonicalCountNamespaces(r.URL.Query()["namespace"])
	jobs := make([]resourceCountJob, 0, len(resourceTypes))
	counts := make(map[string]int64, len(resourceTypes))
	secretCountAuthorized := false
	for _, resourceType := range resourceTypes {
		def := resourceDefs[resourceType]
		resource, ok := rbac.KubernetesResource(resourceType)
		if !ok {
			resource = rbac.ResourceClusters
		}
		verb := rbac.VerbList
		paths := h.authorizedResourceCountPaths(bindings, restricted, clusterID, resourceType, resource, verb, def, requestedNamespaces)
		for _, path := range paths {
			jobs = append(jobs, resourceCountJob{ResourceType: resourceType, Path: path})
		}
		if len(paths) > 0 {
			counts[resourceType] = 0
			secretCountAuthorized = secretCountAuthorized || resourceType == "secrets"
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), resourceCountRequestTimeout)
	defer cancel()
	results := h.runResourceCountJobs(ctx, clusterID.String(), jobs)
	unavailable := map[string]string{}
	for result := range results {
		if result.Err != nil {
			unavailable[result.ResourceType] = "count unavailable"
			delete(counts, result.ResourceType)
			continue
		}
		if _, failed := unavailable[result.ResourceType]; !failed {
			counts[result.ResourceType] += result.Count
		}
	}
	if secretCountAuthorized {
		recordAudit(r, h.queries, "cluster.secret.read", "cluster", clusterID.String(), "secrets", map[string]any{
			"resource_type": "secrets", "verb": string(rbac.VerbList), "scope": "resource_count",
		})
	}
	if len(unavailable) == 0 {
		unavailable = nil
	}
	RespondJSON(w, http.StatusOK, resourceCountsResponse{Counts: counts, Unavailable: unavailable})
}

func parseResourceCountTypes(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		resourceType := strings.ToLower(strings.TrimSpace(part))
		if resourceType == "" {
			continue
		}
		if _, ok := resourceDefs[resourceType]; !ok {
			return nil, fmt.Errorf("unsupported resource type %q", resourceType)
		}
		seen[resourceType] = struct{}{}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("resources is required")
	}
	if len(seen) > maxResourceCountTypes {
		return nil, fmt.Errorf("at most %d resource types may be counted", maxResourceCountTypes)
	}
	out := make([]string, 0, len(seen))
	for resourceType := range seen {
		out = append(out, resourceType)
	}
	sort.Strings(out)
	return out, nil
}

func canonicalCountNamespaces(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, namespace := range strings.Split(value, ",") {
			namespace = strings.TrimSpace(namespace)
			if namespace != "" {
				seen[namespace] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for namespace := range seen {
		out = append(out, namespace)
	}
	sort.Strings(out)
	return out
}

func (h *ResourceHandler) authorizedResourceCountPaths(bindings []rbac.RoleBinding, restricted bool, clusterID uuid.UUID, resourceType string, resource rbac.Resource, verb rbac.Verb, def resourceDef, requestedNamespaces []string) []string {
	if !restricted {
		return resourceCountPaths(resourceType, def, requestedNamespaces)
	}
	if h.authz.engine == nil {
		return nil
	}
	if !def.namespaced {
		if !h.authz.engine.CheckPermission(bindings, resource, verb, clusterID, uuid.Nil) {
			return nil
		}
		return resourceCountPaths(resourceType, def, nil)
	}
	all, allowed := h.authz.engine.AuthorizedNamespaces(bindings, resource, verb, clusterID)
	if all {
		return resourceCountPaths(resourceType, def, requestedNamespaces)
	}
	namespaces := make([]string, 0, len(allowed))
	for namespace := range allowed {
		if len(requestedNamespaces) == 0 || containsSortedString(requestedNamespaces, namespace) {
			namespaces = append(namespaces, namespace)
		}
	}
	sort.Strings(namespaces)
	if len(namespaces) == 0 {
		return nil
	}
	return resourceCountPaths(resourceType, def, namespaces)
}

func resourceCountPaths(resourceType string, def resourceDef, namespaces []string) []string {
	if !def.namespaced || len(namespaces) == 0 {
		path, _ := listPath(resourceType, "")
		return []string{withCountLimit(path)}
	}
	paths := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		path, _ := listPath(resourceType, namespace)
		paths = append(paths, withCountLimit(path))
	}
	return paths
}

func withCountLimit(path string) string {
	parsed, _ := url.Parse(path)
	query := parsed.Query()
	query.Set("limit", "1")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func containsSortedString(values []string, needle string) bool {
	idx := sort.SearchStrings(values, needle)
	return idx < len(values) && values[idx] == needle
}

func (h *ResourceHandler) runResourceCountJobs(ctx context.Context, clusterID string, jobs []resourceCountJob) <-chan resourceCountResult {
	results := make(chan resourceCountResult, len(jobs))
	queue := make(chan resourceCountJob, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	var workers sync.WaitGroup
	workerCount := min(resourceCountConcurrency, len(jobs))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range queue {
				count, err := h.fetchResourceCount(ctx, clusterID, job.Path)
				results <- resourceCountResult{ResourceType: job.ResourceType, Count: count, Err: err}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	return results
}

func (h *ResourceHandler) fetchResourceCount(ctx context.Context, clusterID, path string) (int64, error) {
	headers := requestHeaders("")
	headers["Accept"] = metadataListAccept
	resp, err := h.requester.Do(ctx, clusterID, http.MethodGet, path, nil, headers)
	if err != nil {
		return 0, err
	}
	if err := ensureSuccess(resp); err != nil {
		return 0, err
	}
	var list metadataCountList
	if err := parseJSONResponse(resp, &list); err != nil {
		return 0, err
	}
	count := int64(len(list.Items))
	if list.Metadata.RemainingItemCount != nil {
		return count + *list.Metadata.RemainingItemCount, nil
	}
	if list.Metadata.Continue != "" {
		return 0, fmt.Errorf("apiserver omitted remainingItemCount")
	}
	return count, nil
}
