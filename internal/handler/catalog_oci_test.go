package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestIsOCIRepo(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"oci://ghcr.io/argoproj/argo-helm", true},
		{"OCI://registry-1.docker.io/bitnamicharts", true},
		{"  oci://public.ecr.aws/aws-controllers-k8s  ", true},
		{"https://charts.bitnami.com/bitnami", false},
		{"https://charts.jetstack.io", false},
		{"", false},
		{"oci:/missing-slash", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := IsOCIRepo(tc.in); got != tc.want {
				t.Fatalf("IsOCIRepo(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsOCIRepoSpec(t *testing.T) {
	cases := []struct {
		name string
		repo sqlc.HelmRepository
		want bool
	}{
		{"explicit_repo_type_oci", sqlc.HelmRepository{Url: "https://example.com", RepoType: "oci"}, true},
		{"explicit_repo_type_OCI_uppercase", sqlc.HelmRepository{Url: "https://example.com", RepoType: "OCI"}, true},
		{"url_oci_prefix", sqlc.HelmRepository{Url: "oci://ghcr.io/foo", RepoType: "helm"}, true},
		{"plain_helm", sqlc.HelmRepository{Url: "https://charts.bitnami.com/bitnami", RepoType: "helm"}, false},
		{"empty_repo_type_oci_url", sqlc.HelmRepository{Url: "oci://ghcr.io/foo", RepoType: ""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOCIRepoSpec(tc.repo); got != tc.want {
				t.Fatalf("isOCIRepoSpec(%+v) = %v, want %v", tc.repo, got, tc.want)
			}
		})
	}
}

func TestSplitOCIURL(t *testing.T) {
	cases := []struct {
		url      string
		wantHost string
		wantPath string
		wantErr  bool
	}{
		{"oci://ghcr.io/argoproj/argo-helm", "ghcr.io", "argoproj/argo-helm", false},
		{"oci://registry-1.docker.io/bitnamicharts", "registry-1.docker.io", "bitnamicharts", false},
		{"oci://localhost:5000/charts", "localhost:5000", "charts", false},
		{"https://charts.bitnami.com", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			host, path, err := splitOCIURL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.wantHost || path != tc.wantPath {
				t.Fatalf("split(%q) = (%q, %q), want (%q, %q)", tc.url, host, path, tc.wantHost, tc.wantPath)
			}
		})
	}
}

func TestParseOCIAuthConfig(t *testing.T) {
	cfg := parseOCIAuthConfig([]byte(`{"username":"u","password":"p","charts":["a","b"],"allow_catalog":true}`))
	if cfg.Username != "u" || cfg.Password != "p" {
		t.Fatalf("creds not parsed: %+v", cfg)
	}
	if !cfg.AllowCatalog {
		t.Fatalf("allow_catalog not parsed: %+v", cfg)
	}
	if len(cfg.Charts) != 2 || cfg.Charts[0] != "a" || cfg.Charts[1] != "b" {
		t.Fatalf("charts not parsed: %+v", cfg.Charts)
	}

	// Missing/empty input must not panic and must yield zero value.
	if got := parseOCIAuthConfig(nil); got.Username != "" || len(got.Charts) != 0 {
		t.Fatalf("expected zero value for nil input, got %+v", got)
	}
	if got := parseOCIAuthConfig([]byte(`not-json`)); got.Username != "" {
		t.Fatalf("expected zero value for invalid input, got %+v", got)
	}
}

func TestSelectOCITags(t *testing.T) {
	t.Parallel()

	got := selectOCITags([]string{
		"7.7.10",
		"7.7.12-metadata",
		"7.7.12",
		" 7.7.11 ",
		"",
		"latest",
	}, 3)
	want := []string{"7.7.12", "7.7.11", "7.7.10"}
	if len(got) != len(want) {
		t.Fatalf("len(selectOCITags()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selectOCITags()[%d] = %q, want %q (%v)", i, got[i], want[i], got)
		}
	}
}

func TestOCIMetadataFromPull(t *testing.T) {
	// Construct a synthetic Helm chart manifest and verify we extract the
	// fields we persist on HelmChart. We use the public helm types directly
	// so the fixture stays in lockstep with the SDK.
	pull := &registry.PullResult{
		Manifest: &registry.DescriptorPullSummary{Digest: "sha256:abcd"},
		Chart: &registry.DescriptorPullSummaryWithMeta{
			Meta: &chart.Metadata{
				Name:        "argo-cd",
				Version:     "5.51.0",
				AppVersion:  "v2.9.3",
				Description: "A GitOps continuous delivery tool.",
				Icon:        "https://argo-cd.example/icon.png",
				Home:        "https://argo-cd.example",
				Keywords:    []string{"gitops", "cd"},
				Maintainers: []*chart.Maintainer{
					{Name: "alice", Email: "a@example.com"},
				},
				Deprecated: false,
			},
		},
	}
	got := ociMetadataFromPull(pull)
	if got.description != "A GitOps continuous delivery tool." {
		t.Fatalf("description: %q", got.description)
	}
	if got.icon != "https://argo-cd.example/icon.png" {
		t.Fatalf("icon: %q", got.icon)
	}
	if got.home != "https://argo-cd.example" {
		t.Fatalf("home: %q", got.home)
	}
	if len(got.keywords) != 2 || got.keywords[0] != "gitops" {
		t.Fatalf("keywords: %v", got.keywords)
	}
	if len(got.maintainers) != 1 || got.maintainers[0].Name != "alice" {
		t.Fatalf("maintainers: %+v", got.maintainers)
	}

	// Nil-safe.
	if zero := ociMetadataFromPull(nil); zero.description != "" {
		t.Fatalf("expected zero meta for nil pull, got %+v", zero)
	}
	if zero := ociMetadataFromPull(&registry.PullResult{}); zero.description != "" {
		t.Fatalf("expected zero meta for empty pull, got %+v", zero)
	}
}

// TestOCIAuthConfigJSONRoundTrip ensures the on-disk representation we
// document for operators round-trips cleanly. This guards against accidental
// renames of the JSON tags we expose.
func TestOCIAuthConfigJSONRoundTrip(t *testing.T) {
	in := `{"username":"x","password":"y","charts":["argo-cd","argo-workflows"],"allow_catalog":false}`
	cfg := parseOCIAuthConfig([]byte(in))
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"username":"x"`, `"password":"y"`, `"argo-cd"`, `"argo-workflows"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("round-trip lost %q: %s", want, string(out))
		}
	}
}

// TestFetchAndIngestOCIRepoNoCharts checks that we surface a clear error
// when the operator gives us an OCI repo with neither an explicit chart
// list nor /v2/_catalog opt-in.
func TestFetchAndIngestOCIRepoNoCharts(t *testing.T) {
	h := NewCatalogHandler(nil)
	repo := sqlc.HelmRepository{
		Url:        "oci://ghcr.io/argoproj/argo-helm",
		RepoType:   "oci",
		AuthConfig: []byte(`{}`),
	}
	_, _, err := h.fetchAndIngestOCIRepo(t.Context(), repo)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "auth_config.charts") {
		t.Fatalf("error should reference auth_config.charts, got: %v", err)
	}
}

// TestFetchAndIngestOCIRepoLive is intentionally skipped — running it would
// require network access to a real OCI registry, and the test environment
// has no such guarantee. Restore by removing the Skip and pointing it at a
// reachable registry to validate end-to-end ingest manually.
func TestFetchAndIngestOCIRepoLive(t *testing.T) {
	t.Skip("network-dependent; run manually against a reachable OCI registry")
}
