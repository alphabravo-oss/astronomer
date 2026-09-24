package handler

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

// selectedNamespaces distinguishes omitted scope from explicit empty scope.
// Repeated namespaces parameters are intersected with permission-derived scope.
func selectedNamespaces(query url.Values, all bool, allowed map[string]struct{}) (bool, map[string]struct{}, error) {
	values, present := query["namespaces"]
	if len(query["namespace"]) > 1 {
		return false, nil, fmt.Errorf("Use namespaces for multiple namespace values")
	}
	single := query.Get("namespace")
	if !present && single == "" {
		return all, allowed, nil
	}
	if present && single != "" {
		return false, nil, fmt.Errorf("Use namespace or namespaces, not both")
	}
	if !present {
		values = []string{single}
	}
	if len(values) > 100 {
		return false, nil, fmt.Errorf("Select at most 100 namespaces")
	}
	selected := make(map[string]struct{}, len(values))
	for _, name := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			if len(values) == 1 {
				continue
			}
			return false, nil, fmt.Errorf("Empty namespace cannot be combined with named namespaces")
		}
		if len(k8svalidation.IsDNS1123Label(name)) != 0 {
			return false, nil, fmt.Errorf("Invalid Kubernetes namespace")
		}
		if _, ok := allowed[name]; all || ok {
			selected[name] = struct{}{}
		}
	}
	return false, selected, nil
}

func namespaceNames(all bool, names map[string]struct{}) []string {
	if all {
		return []string{""}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
