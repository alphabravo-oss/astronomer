package catalog

// Moved here with their subjects (SelectOCITags / OCIMetadataFromPull) when
// the OCI ingest left internal/handler so the scheduled catalog:sync sweep
// could reach it. The remaining OCI tests still live in
// internal/handler/catalog_oci_test.go against the handler-side wrappers.

import (
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/registry"
)

func TestSelectOCITags(t *testing.T) {
	t.Parallel()

	got := SelectOCITags([]string{
		"7.7.10",
		"7.7.12-metadata",
		"7.7.12",
		" 7.7.11 ",
		"",
		"latest",
	}, 3)
	want := []string{"7.7.12", "7.7.11", "7.7.10"}
	if len(got) != len(want) {
		t.Fatalf("len(SelectOCITags()) = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SelectOCITags()[%d] = %q, want %q (%v)", i, got[i], want[i], got)
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
	got := OCIMetadataFromPull(pull)
	if got.Description != "A GitOps continuous delivery tool." {
		t.Fatalf("description: %q", got.Description)
	}
	if got.Icon != "https://argo-cd.example/icon.png" {
		t.Fatalf("icon: %q", got.Icon)
	}
	if got.Home != "https://argo-cd.example" {
		t.Fatalf("home: %q", got.Home)
	}
	if len(got.Keywords) != 2 || got.Keywords[0] != "gitops" {
		t.Fatalf("keywords: %v", got.Keywords)
	}
	if len(got.Maintainers) != 1 || got.Maintainers[0].Name != "alice" {
		t.Fatalf("maintainers: %+v", got.Maintainers)
	}

	// Nil-safe.
	if zero := OCIMetadataFromPull(nil); zero.Description != "" {
		t.Fatalf("expected zero meta for nil pull, got %+v", zero)
	}
	if zero := OCIMetadataFromPull(&registry.PullResult{}); zero.Description != "" {
		t.Fatalf("expected zero meta for empty pull, got %+v", zero)
	}
}
