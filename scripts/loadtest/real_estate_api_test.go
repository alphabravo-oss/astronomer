package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func TestRealEstateVerifiesReadOnlyFixtures(t *testing.T) {
	now := time.Now().UTC()
	m := estateTestManifest().Members[0]
	client := estateFixtureClient(t, m, now)
	got, err := verifyEstateMember(context.Background(), client, "https://api.test", "api-test-token", m, now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IdentityVerified || got.RenderedNamespace != "UNAVAILABLE" || got.Census["pods"] != 1 {
		t.Fatalf("wrong proof: %+v", got)
	}
	m.Resources["pods"] = 2
	if _, err = verifyEstateMember(context.Background(), client, "https://api.test", "api-test-token", m, now); err == nil {
		t.Fatal("wrong census accepted")
	}
}
func TestRealEstateScopeMismatch(t *testing.T) {
	now := time.Now()
	m := estateTestManifest().Members[0]
	c := estateFixtureClient(t, m, now)
	original := c.Transport
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/delivery/targets/") {
			return estateResponse(map[string]any{"data": map[string]any{"id": m.Assignments[0].TargetID, "project_id": "wrong", "deletion_state": "active"}}), nil
		}
		return original.RoundTrip(r)
	})
	if _, err := verifyEstateMember(context.Background(), c, "https://api.test", "api-test-token", m, now); err == nil {
		t.Fatal("cross-project target accepted")
	}
}
func TestRealEstateMetricsCredentialsAndRedirect(t *testing.T) {
	c := estateHTTPClient()
	calls := 0
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer metric-token" {
			t.Fatal("wrong credential")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.test/metrics"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if _, err := readEstateMetrics(context.Background(), c, "https://metrics.test/metrics", "metric-token"); err == nil || calls != 1 {
		t.Fatal("redirect followed or accepted")
	}
}
func TestRealEstateRenderedNamespaceNeedsSourceProof(t *testing.T) {
	now := time.Now()
	m := estateTestManifest().Members[0]
	a := m.Assignments[0]
	ref := astroclient.DeliveryResourceIdentity{ApiVersion: "apps/v1", Kind: "Deployment", Name: "fixture", Namespace: &m.Namespace}
	a.RenderedResources = []astroclient.DeliveryResourceIdentity{ref}
	refs := []astroclient.DeliveryResourceIdentity{ref}
	inv := astroclient.DeliveryResourceInventory{Entries: 1, Resources: &refs}
	c := estateHTTPClient()
	calls := 0
	c.Transport = estateRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/deployments/fixture") {
			t.Fatal("unexpected rendered GET")
		}
		return estateResponse(map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "fixture", "namespace": m.Namespace, "uid": "uid"}}), nil
	})
	if ok, err := verifyEstateRendered(context.Background(), c, "https://api.test", "", m, a, inv, now); ok || err != nil || calls != 0 {
		t.Fatal("legacy receipt treated as source proof")
	}
	inv.Observation = &astroclient.DeliveryObservation{State: "current", ObservedAt: &now}
	if ok, err := verifyEstateRendered(context.Background(), c, "https://api.test", "", m, a, inv, now); !ok || err != nil || calls != 1 {
		t.Fatal("modern namespace verification failed", err)
	}
	inv.Entries = 2
	if ok, err := verifyEstateRendered(context.Background(), c, "https://api.test", "", m, a, inv, now); ok || err != nil {
		t.Fatal("truncated inventory verified")
	}
	inv.Entries = 1
	old := now.Add(-6 * time.Minute)
	inv.Observation.ObservedAt = &old
	if ok, _ := verifyEstateRendered(context.Background(), c, "https://api.test", "", m, a, inv, now); ok {
		t.Fatal("stale source verified")
	}
}
