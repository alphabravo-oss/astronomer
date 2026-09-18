package handler

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

// ListProfiles handles GET /api/v1/security/profiles/?cluster_id=X — returns
// the set of `ClusterScanProfile` CRs currently installed on the target
// cluster (cis-operator preinstalls a few). When the K8s requester is unset
// we return the static fallback set so the UI always renders something.
func (h *SecurityHandler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	clusterID, present, ok := parseOptionalClusterID(w, r)
	if !ok {
		return
	}
	if !present {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cluster_id query parameter is required")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, rbac.VerbRead) {
		return
	}
	if h.k8s == nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
		})
		return
	}
	resp, err := h.k8s.Do(r.Context(), clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanprofiles", nil, requestHeaders(""))
	if err != nil {
		// Best-effort fallback so the UI keeps working when the operator
		// isn't installed yet.
		h.log.Warn("list ClusterScanProfile CRs failed", "cluster_id", clusterID.String(), "error", err)
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
			"error":  err.Error(),
		})
		return
	}
	if err := ensureSuccess(resp); err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"items":  staticCISProfiles(),
			"source": "fallback",
			"error":  err.Error(),
		})
		return
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Benchmark string `json:"benchmarkVersion"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.InvalidBody, "Failed to decode ClusterScanProfile list")
		return
	}
	items := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		items = append(items, map[string]any{
			"name":             item.Metadata.Name,
			"benchmarkVersion": item.Spec.Benchmark,
		})
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"source": "cluster",
	})
}

type cisClusterScanProfile struct {
	Name      string
	Benchmark string
}

func (h *SecurityHandler) resolveClusterScanProfileName(ctx context.Context, clusterID uuid.UUID, distribution, profile string) string {
	profile = strings.TrimSpace(profile)
	profiles, err := h.listClusterScanProfiles(ctx, clusterID)
	if err != nil || len(profiles) == 0 {
		return profile
	}
	if profile != "" {
		for _, item := range profiles {
			if item.Name == profile || item.Benchmark == profile {
				return item.Name
			}
		}
		return profile
	}
	if recommended, ok := recommendClusterScanProfileName(distribution, profiles); ok {
		return recommended
	}
	return profile
}

func (h *SecurityHandler) listClusterScanProfiles(ctx context.Context, clusterID uuid.UUID) ([]cisClusterScanProfile, error) {
	if h == nil || h.k8s == nil {
		return nil, nil
	}
	resp, err := h.k8s.Do(ctx, clusterID.String(), http.MethodGet,
		"/apis/cis.cattle.io/v1/clusterscanprofiles", nil, requestHeaders(""))
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Benchmark string `json:"benchmarkVersion"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := parseJSONResponse(resp, &list); err != nil {
		return nil, err
	}
	out := make([]cisClusterScanProfile, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, cisClusterScanProfile{
			Name:      item.Metadata.Name,
			Benchmark: item.Spec.Benchmark,
		})
	}
	return out, nil
}

var cisBenchmarkVersionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

func recommendClusterScanProfileName(distribution string, profiles []cisClusterScanProfile) (string, bool) {
	prefix := cisProfileBenchmarkPrefix(distribution)
	bestName := ""
	bestMajor := -1
	bestMinor := -1
	bestPermissive := false
	for _, profile := range profiles {
		if !strings.HasPrefix(profile.Benchmark, prefix) {
			continue
		}
		major, minor := parseCISBenchmarkVersion(profile.Benchmark)
		permissive := strings.Contains(profile.Name, "permissive") || strings.Contains(profile.Benchmark, "permissive")
		if bestName == "" ||
			major > bestMajor ||
			(major == bestMajor && minor > bestMinor) ||
			(major == bestMajor && minor == bestMinor && permissive && !bestPermissive) {
			bestName = profile.Name
			bestMajor = major
			bestMinor = minor
			bestPermissive = permissive
		}
	}
	if bestName != "" {
		return bestName, true
	}
	if prefix != "cis-" {
		return recommendClusterScanProfileName("", profiles)
	}
	return "", false
}

func cisProfileBenchmarkPrefix(distribution string) string {
	switch strings.ToLower(strings.TrimSpace(distribution)) {
	case "rke", "rke1":
		return "rke-"
	case "rke2":
		return "rke2-"
	case "k3s":
		return "k3s-"
	case "eks":
		return "eks-"
	case "aks":
		return "aks-"
	case "gke":
		return "gke-"
	default:
		return "cis-"
	}
}

func parseCISBenchmarkVersion(s string) (int, int) {
	matches := cisBenchmarkVersionRE.FindStringSubmatch(s)
	if len(matches) != 3 {
		return -1, -1
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1, -1
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return -1, -1
	}
	return major, minor
}

// defaultCISProfileForDistribution maps the distribution string the cluster
// row carries to the CIS profile that ships preinstalled with cis-operator.
// Falls back to cis-1.8 for unknown distributions.
func defaultCISProfileForDistribution(distribution string) string {
	switch strings.ToLower(strings.TrimSpace(distribution)) {
	case "rke", "rke1":
		return "rke-profile-permissive-1.8"
	case "rke2":
		return "rke2-cis-1.8-profile-permissive"
	case "k3s":
		return "k3s-cis-1.8-profile-permissive"
	case "eks":
		return "eks-profile-1.5.0"
	case "aks":
		return "aks-profile"
	case "gke":
		return "gke-profile-1.6.0"
	default:
		return "cis-1.8-profile"
	}
}

func staticCISProfiles() []map[string]any {
	return []map[string]any{
		{"name": "cis-1.8", "benchmarkVersion": "cis-1.8"},
		{"name": "rke-cis-1.8-permissive", "benchmarkVersion": "rke-cis-1.8"},
		{"name": "rke2-cis-1.8-permissive", "benchmarkVersion": "rke2-cis-1.8"},
		{"name": "k3s-cis-1.8-permissive", "benchmarkVersion": "k3s-cis-1.8"},
		{"name": "eks-cis-1.5", "benchmarkVersion": "eks-cis-1.5"},
		{"name": "aks-cis-1.0", "benchmarkVersion": "aks-cis-1.0"},
		{"name": "gke-cis-1.5", "benchmarkVersion": "gke-cis-1.5"},
	}
}
