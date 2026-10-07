package kuberequests

import (
	"net/http"
	"strconv"
	"strings"
)

// Only known API group/resource pairs reach a label. Names, namespaces, versions,
// queries, subresource names and unknown groups never become metric values.
var resources = map[string]map[string]bool{
	"":                            {"pods": true, "persistentvolumeclaims": true},
	"apps":                        {"deployments": true, "statefulsets": true, "daemonsets": true},
	"storage.k8s.io":              {"storageclasses": true},
	"apiextensions.k8s.io":        {"customresourcedefinitions": true},
	"source.toolkit.fluxcd.io":    {"gitrepositories": true, "ocirepositories": true, "helmrepositories": true},
	"kustomize.toolkit.fluxcd.io": {"kustomizations": true},
	"helm.toolkit.fluxcd.io":      {"helmreleases": true},
	"longhorn.io":                 {"volumes": true, "nodes": true},
	"cert-manager.io":             {"certificates": true},
	"gateway.networking.k8s.io":   {"gateways": true},
	"snapshot.storage.k8s.io":     {"volumesnapshots": true},
}

func classify(r *http.Request) (string, string) {
	if r.URL == nil || r.Method != http.MethodGet {
		return "other", "other"
	}
	path := r.URL.EscapedPath()
	if strings.Contains(path, "%") || strings.Contains(path, "//") {
		return "other", "other"
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if strings.TrimSuffix(path, "/") == "/version" || strings.TrimSuffix(path, "/") == "/api" || strings.TrimSuffix(path, "/") == "/apis" || (len(parts) == 2 && parts[0] == "apis" && parts[1] != "") {
		return "discovery", "other"
	}
	group := ""
	offset := 0
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "v1" {
		offset = 2
	} else if len(parts) >= 3 && parts[0] == "apis" && parts[1] != "" && parts[2] != "" {
		group = parts[1]
		offset = 3
	} else {
		return "other", "other"
	}
	if len(parts) == offset {
		return "discovery", "other"
	}
	rest := parts[offset:]
	if len(rest) >= 3 && rest[0] == "namespaces" {
		rest = rest[2:]
	}
	if len(rest) == 0 || !resources[group][rest[0]] {
		return "other", "other"
	}
	resource := rest[0]
	if len(rest) > 2 {
		return "other", resource
	}
	watching, err := strconv.ParseBool(r.URL.Query().Get("watch"))
	if err == nil && watching {
		return "watch", resource
	}
	if len(rest) == 1 {
		return "list", resource
	}
	return "get", resource
}
