package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

type crdDiscoveryPage struct {
	limit         int
	continueToken string
}

func parseCRDDiscoveryPage(r *http.Request) (crdDiscoveryPage, error) {
	page := crdDiscoveryPage{limit: 500, continueToken: r.URL.Query().Get("crd_continue")}
	if values, ok := r.URL.Query()["crd_limit"]; ok {
		if len(values) != 1 {
			return page, errors.New("crd_limit must occur once")
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 500 {
			return page, errors.New("crd_limit must be between 1 and 500")
		}
		page.limit = limit
	}
	if len(r.URL.Query()["crd_continue"]) > 1 || len(page.continueToken) > 32768 {
		return page, errors.New("crd_continue must be one opaque cursor of at most 32768 bytes")
	}
	return page, nil
}

// A continuation retrieves one CRD metadata page only. The first page carries
// builtins; callers retain those while accumulating subsequent CRD summaries.
func (h *ResourceHandler) discoverCRDPage(r *http.Request, clusterID string, page crdDiscoveryPage) ([]map[string]any, string, error) {
	query := url.Values{"limit": {strconv.Itoa(page.limit)}}
	if page.continueToken != "" {
		query.Set("continue", page.continueToken)
	}
	payload, err := h.do(r.Context(), clusterID, http.MethodGet, "/apis/apiextensions.k8s.io/v1/customresourcedefinitions?"+query.Encode(), nil, requestHeaders(""))
	if err != nil {
		return nil, "", err
	}
	next, err := validateCRDDiscoveryPage(payload, page.limit)
	if err != nil {
		return nil, "", err
	}
	return summarizeDiscoveredCRDs(payload), next, nil
}

func validateCRDDiscoveryPage(payload map[string]any, limit int) (string, error) {
	items, ok := payload["items"].([]any)
	if !ok || len(items) > limit {
		return "", errors.New("invalid CRD discovery page: missing items or page limit exceeded")
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return "", errors.New("invalid CRD discovery item")
		}
		metadata, _ := item["metadata"].(map[string]any)
		spec, _ := item["spec"].(map[string]any)
		names, _ := spec["names"].(map[string]any)
		_, versionsOK := spec["versions"].([]any)
		scope := scalarString(spec["scope"])
		if scalarString(metadata["name"]) == "" || scalarString(spec["group"]) == "" || scalarString(names["plural"]) == "" || scalarString(names["kind"]) == "" || !versionsOK || (scope != "Namespaced" && scope != "Cluster") {
			return "", errors.New("invalid CRD discovery item coordinates")
		}
	}
	metadata, ok := payload["metadata"].(map[string]any)
	if !ok {
		return "", errors.New("invalid CRD discovery page metadata")
	}
	next := ""
	if value, present := metadata["continue"]; present {
		next, ok = value.(string)
		if !ok || len(next) > 32768 {
			return "", errors.New("invalid CRD discovery continuation")
		}
	}
	return next, nil
}

func discoveryErrorStatus(err error) int {
	var upstream *kubernetesResponseError
	if errors.As(err, &upstream) {
		switch upstream.StatusCode {
		case http.StatusBadRequest, http.StatusForbidden, http.StatusGone, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			return upstream.StatusCode
		}
	}
	return http.StatusBadGateway
}

func (h *ResourceHandler) discoverBuiltinResources(r *http.Request, clusterID string) ([]resourceDiscoveryEntry, map[string]string) {
	type discoveryResult struct {
		payload map[string]any
		err     error
	}
	byAPI := map[string]discoveryResult{}
	entries := make([]resourceDiscoveryEntry, 0, len(enterpriseResourceMatrix))
	errorsBySource := map[string]string{}
	for _, resourceType := range enterpriseResourceMatrix {
		def := resourceDefs[resourceType]
		result, loaded := byAPI[def.apiBase]
		if !loaded {
			result.payload, result.err = h.do(r.Context(), clusterID, http.MethodGet, def.apiBase, nil, requestHeaders(""))
			byAPI[def.apiBase] = result
		}
		entry, err := resourceDiscoveryEntry{}, result.err
		if err == nil {
			entry, err = resourceDiscoveryFromPayload(resourceType, def, result.payload)
		}
		if err != nil {
			entry = fallbackResourceDiscovery(resourceType, def)
			errorsBySource[resourceType] = fmt.Sprint(err)
		}
		entries = append(entries, entry)
	}
	return entries, errorsBySource
}
