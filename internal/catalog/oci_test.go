package catalog

// Moved here with their subjects (SelectOCITags / OCIMetadataFromPull) when
// the OCI ingest left internal/handler so the scheduled catalog:sync sweep
// could reach it. The remaining OCI tests still live in
// internal/handler/catalog_oci_test.go against the handler-side wrappers.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/registry"
)

type ociRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ociRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

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
				Name:        "cert-manager",
				Version:     "5.51.0",
				AppVersion:  "v2.9.3",
				Description: "A GitOps continuous delivery tool.",
				Icon:        "https://cert-manager.example/icon.png",
				Home:        "https://cert-manager.example",
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
	if got.Icon != "https://cert-manager.example/icon.png" {
		t.Fatalf("icon: %q", got.Icon)
	}
	if got.Home != "https://cert-manager.example" {
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

func TestListOCIRegistryTagsRejectsTotalOverflow(t *testing.T) {
	tags := make([]string, 0, ociTagMaxTotal+1)
	for index := 0; index <= ociTagMaxTotal; index++ {
		tags = append(tags, fmt.Sprintf("1.0.%d", index))
	}
	body := `{"name":"team/widget","tags":["` + strings.Join(tags, `","`) + `"]}`
	client := &http.Client{Transport: ociRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	if _, err := listOCIRegistryTags(context.Background(), "registry.example.test/team/widget", OCIAuthConfig{}, client); err == nil || !strings.Contains(err.Error(), "total limit") {
		t.Fatalf("oversized tag set was accepted: %v", err)
	}
}

func TestListOCIRegistryTagsRejectsExcessivePagination(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: ociRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		header := make(http.Header)
		header.Set("Link", `</v2/team/widget/tags/list?n=100&last=1.0.0>; rel="next"`)
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(`{"name":"team/widget","tags":["1.0.0"]}`)), Request: request}, nil
	})}
	if _, err := listOCIRegistryTags(context.Background(), "registry.example.test/team/widget", OCIAuthConfig{}, client); err == nil || !strings.Contains(err.Error(), "pages") {
		t.Fatalf("endless tag pagination was accepted: %v", err)
	}
	if requests != ociTagMaxPages {
		t.Fatalf("registry requests=%d, want hard cap %d", requests, ociTagMaxPages)
	}
}

func TestNormalizeOCIChartNamesBoundsAndValidatesInput(t *testing.T) {
	got, err := normalizeOCIChartNames([]string{" team/widget ", "team/widget", "metrics.v2"})
	if err != nil || len(got) != 2 || got[0] != "team/widget" || got[1] != "metrics.v2" {
		t.Fatalf("normalizeOCIChartNames() = %v, %v", got, err)
	}
	if _, err := normalizeOCIChartNames([]string{"../metadata"}); err == nil {
		t.Fatal("unsafe OCI chart name was accepted")
	}
	tooMany := make([]string, ociChartMaxCount+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("chart-%d", index)
	}
	if _, err := normalizeOCIChartNames(tooMany); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized OCI chart list was accepted: %v", err)
	}
}
