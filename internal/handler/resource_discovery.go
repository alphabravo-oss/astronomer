package handler

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

// resourcePolicyMetadata is Astronomer's management-plane policy layered on
// top of Kubernetes discovery. It lets API and UI clients distinguish normal
// edits from actions that deserve stronger confirmation or permissions.
type resourcePolicyMetadata struct {
	Secret                  bool `json:"secret"`
	PrivilegeEscalating     bool `json:"privilege_escalating"`
	DestructiveDelete       bool `json:"destructive_delete"`
	ForceConflictPermission bool `json:"force_conflict_permission"`
}

type resourceDiscoveryEntry struct {
	ResourceType string                 `json:"resource_type"`
	APIBase      string                 `json:"api_base"`
	APIGroup     string                 `json:"api_group,omitempty"`
	APIVersion   string                 `json:"api_version"`
	Kind         string                 `json:"kind"`
	Plural       string                 `json:"plural"`
	Namespaced   bool                   `json:"namespaced"`
	Verbs        []string               `json:"verbs"`
	ShortNames   []string               `json:"short_names,omitempty"`
	Categories   []string               `json:"categories,omitempty"`
	Policy       resourcePolicyMetadata `json:"policy"`
	Source       string                 `json:"source"`
}

var enterpriseResourceMatrix = []string{
	"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs",
	"services", "ingresses", "gateways", "configmaps", "secrets",
	"persistentvolumeclaims", "namespaces", "serviceaccounts", "k8s-roles",
	"k8s-rolebindings", "networkpolicies", "hpa", "poddisruptionbudgets",
}

// GetResourceDiscovery combines the supported management matrix with live CRD
// discovery. It is intentionally a management API instead of browser-direct
// Kubernetes discovery, so authorization, response bounds, and policy labels
// remain consistent across UI, CLI, and automation clients.
func (h *ResourceHandler) GetResourceDiscovery(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	if clusterID == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "cluster_id is required")
		return
	}
	entries := make([]resourceDiscoveryEntry, 0, len(enterpriseResourceMatrix))
	errorsBySource := map[string]string{}
	for _, resourceType := range enterpriseResourceMatrix {
		entry, err := h.discoverResource(r, clusterID, resourceType)
		if err != nil {
			def := resourceDefs[resourceType]
			entry = fallbackResourceDiscovery(resourceType, def)
			errorsBySource[resourceType] = err.Error()
		}
		entries = append(entries, entry)
	}

	crds := []map[string]any{}
	crdPayload, err := h.do(r.Context(), clusterID, http.MethodGet,
		"/apis/apiextensions.k8s.io/v1/customresourcedefinitions?limit=500", nil, requestHeaders(""))
	if err != nil {
		errorsBySource["custom_resource_definitions"] = err.Error()
	} else {
		crds = summarizeDiscoveredCRDs(crdPayload)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id": clusterID,
		"resources":  entries,
		"crds":       crds,
		"partial":    len(errorsBySource) > 0,
		"errors":     errorsBySource,
	})
}

// GetResourceSchema returns the live structural schema and API discovery data
// for one supported resource. Clients use it for guided forms and validation;
// raw YAML remains an expert-mode projection of the same API contract.
func (h *ResourceHandler) GetResourceSchema(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("resource_type")))
	def, ok := resourceDefs[resourceType]
	if clusterID == "" || resourceType == "" || !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, "a supported resource_type is required")
		return
	}
	entry, err := h.discoverResource(r, clusterID, resourceType)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, err.Error())
		return
	}
	result := map[string]any{"resource": entry, "schema": map[string]any{}, "schema_available": false}
	index, err := h.do(r.Context(), clusterID, http.MethodGet, "/openapi/v3", nil, requestHeaders(""))
	if err == nil {
		key := strings.TrimPrefix(def.apiBase, "/")
		if paths, ok := index["paths"].(map[string]any); ok {
			if descriptor, ok := paths[key].(map[string]any); ok {
				if relative, _ := descriptor["serverRelativeURL"].(string); strings.HasPrefix(relative, "/openapi/v3/") {
					if document, docErr := h.do(r.Context(), clusterID, http.MethodGet, relative, nil, requestHeaders("")); docErr == nil {
						if schema, name := findResourceOpenAPISchema(document, entry.Kind); schema != nil {
							result["schema"] = schema
							result["schema_name"] = name
							definitions, truncated := referencedResourceSchemas(document, schema, 256)
							result["definitions"] = definitions
							result["definitions_truncated"] = truncated
							result["schema_available"] = true
						}
					}
				}
			}
		}
	}
	RespondJSON(w, http.StatusOK, result)
}

func (h *ResourceHandler) discoverResource(r *http.Request, clusterID, resourceType string) (resourceDiscoveryEntry, error) {
	def, ok := resourceDefs[resourceType]
	if !ok {
		return resourceDiscoveryEntry{}, fmt.Errorf("unsupported resource type %q", resourceType)
	}
	payload, err := h.do(r.Context(), clusterID, http.MethodGet, def.apiBase, nil, requestHeaders(""))
	if err != nil {
		return resourceDiscoveryEntry{}, err
	}
	resources, _ := payload["resources"].([]any)
	for _, raw := range resources {
		item, _ := raw.(map[string]any)
		if scalarString(item["name"]) != def.plural {
			continue
		}
		group, version := splitAPIBase(def.apiBase)
		return resourceDiscoveryEntry{
			ResourceType: resourceType, APIBase: def.apiBase, APIGroup: group,
			APIVersion: version, Kind: scalarString(item["kind"]), Plural: def.plural,
			Namespaced: boolFromAny(item["namespaced"]), Verbs: scalarStringSlice(item["verbs"]),
			ShortNames: scalarStringSlice(item["shortNames"]), Categories: scalarStringSlice(item["categories"]),
			Policy: resourcePolicyFor(resourceType, group), Source: "kubernetes_discovery",
		}, nil
	}
	return resourceDiscoveryEntry{}, fmt.Errorf("resource %q is not served by %s", def.plural, def.apiBase)
}

func fallbackResourceDiscovery(resourceType string, def resourceDef) resourceDiscoveryEntry {
	group, version := splitAPIBase(def.apiBase)
	return resourceDiscoveryEntry{
		ResourceType: resourceType, APIBase: def.apiBase, APIGroup: group,
		APIVersion: version, Plural: def.plural, Namespaced: def.namespaced,
		Policy: resourcePolicyFor(resourceType, group), Source: "astronomer_fallback",
	}
}

func splitAPIBase(base string) (group, version string) {
	parts := strings.Split(strings.Trim(base, "/"), "/")
	if len(parts) == 2 && parts[0] == "api" {
		return "", parts[1]
	}
	if len(parts) >= 3 && parts[0] == "apis" {
		return parts[1], parts[2]
	}
	return "", ""
}

func resourcePolicyFor(resourceType, group string) resourcePolicyMetadata {
	policy := resourcePolicyMetadata{ForceConflictPermission: true}
	switch resourceType {
	case "secrets":
		policy.Secret = true
	case "persistentvolumeclaims", "namespaces":
		policy.DestructiveDelete = true
	}
	policy.PrivilegeEscalating = isPrivilegeEscalatingResourceGroup(group) ||
		resourceType == "k8s-roles" || resourceType == "k8s-rolebindings"
	return policy
}

func isPrivilegeEscalatingResourceGroup(group string) bool {
	switch group {
	case "rbac.authorization.k8s.io", "admissionregistration.k8s.io",
		"apiregistration.k8s.io", "apiextensions.k8s.io",
		"certificates.k8s.io", "authentication.k8s.io":
		return true
	}
	return false
}

func summarizeDiscoveredCRDs(payload map[string]any) []map[string]any {
	items, _ := payload["items"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		metadata, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		names, _ := spec["names"].(map[string]any)
		versions := []map[string]any{}
		if rawVersions, ok := spec["versions"].([]any); ok {
			for _, rawVersion := range rawVersions {
				version, _ := rawVersion.(map[string]any)
				if !boolFromAny(version["served"]) {
					continue
				}
				versions = append(versions, map[string]any{
					"name": scalarString(version["name"]), "storage": boolFromAny(version["storage"]),
					"printer_columns": version["additionalPrinterColumns"],
				})
			}
		}
		out = append(out, map[string]any{
			"name": scalarString(metadata["name"]), "group": scalarString(spec["group"]),
			"scope": scalarString(spec["scope"]), "kind": scalarString(names["kind"]),
			"plural": scalarString(names["plural"]), "short_names": names["shortNames"],
			"categories": names["categories"], "versions": versions,
		})
	}
	return out
}

func findResourceOpenAPISchema(document map[string]any, kind string) (map[string]any, string) {
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for name, raw := range schemas {
		schema, _ := raw.(map[string]any)
		gvks, _ := schema["x-kubernetes-group-version-kind"].([]any)
		for _, rawGVK := range gvks {
			gvk, _ := rawGVK.(map[string]any)
			if scalarString(gvk["kind"]) == kind {
				return schema, name
			}
		}
	}
	return nil, ""
}

// referencedResourceSchemas returns the transitive local-schema closure needed
// to interpret a selected Kubernetes schema. Returning only the root leaves
// fields such as Deployment.spec behind unresolved $refs; returning the entire
// API-group document can be several megabytes. The bounded closure gives API,
// UI, and CLI clients a useful self-contained contract without an unbounded
// management-plane response.
func referencedResourceSchemas(document, root map[string]any, limit int) (map[string]any, bool) {
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	definitions := make(map[string]any)
	queued := make(map[string]bool)
	queue := schemaReferences(root)
	for _, name := range queue {
		queued[name] = true
	}
	truncated := false
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, exists := definitions[name]; exists {
			continue
		}
		if len(definitions) >= limit {
			truncated = true
			break
		}
		schema, ok := schemas[name]
		if !ok {
			continue
		}
		definitions[name] = schema
		for _, child := range schemaReferences(schema) {
			if !queued[child] {
				queued[child] = true
				queue = append(queue, child)
			}
		}
	}
	return definitions, truncated
}

func schemaReferences(value any) []string {
	seen := make(map[string]bool)
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if ref, _ := typed["$ref"].(string); strings.HasPrefix(ref, "#/components/schemas/") {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
				seen[name] = true
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func scalarString(value any) string {
	text, _ := value.(string)
	return text
}

func scalarStringSlice(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
