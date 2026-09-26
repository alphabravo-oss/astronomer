package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAppCasesCoverFrozenCatalog(t *testing.T) {
	manifest, err := loadCases("../testdata/offering-qualification/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, definition := range manifest.Cases {
		if definition.Category == "app" {
			want[definition.ID] = true
		}
	}
	if len(want) != 21 || len(appCases) != len(want) {
		t.Fatalf("app executor count=%d manifest count=%d", len(appCases), len(want))
	}
	for id, slug := range appCases {
		if !want[id] || slug == "" {
			t.Fatalf("invalid app executor mapping %s=%q", id, slug)
		}
		spec := appSpec(slug, memberTarget{Namespace: "qualification"}, "1.2.3")
		if spec.Namespace == "" || spec.ReleaseName == "" {
			t.Fatalf("%s has incomplete install spec: %#v", slug, spec)
		}
	}
}

func TestConstellationUsesSystemNamespace(t *testing.T) {
	spec := appSpec("constellation", memberTarget{Namespace: "project-isolated"}, "0.2.0")
	if spec.Namespace != "astronomer-constellation" {
		t.Fatalf("constellation namespace = %q, want dedicated system namespace", spec.Namespace)
	}
	if spec.Values != "image:\n  tag: v0.2.0\n" {
		t.Fatalf("constellation values = %q, want the published v-prefixed release tag", spec.Values)
	}
	if got := appSpec("constellation", memberTarget{}, "v0.2.0").Values; got != spec.Values {
		t.Fatalf("v-prefixed catalog version was changed: %q", got)
	}
}

func TestKubePrometheusStackAvoidsBaselineExporterOwnership(t *testing.T) {
	spec := appSpec("kube-prometheus-stack", memberTarget{}, "88.5.4")
	for _, required := range []string{"nodeExporter:\n  enabled: false", "kubeStateMetrics:\n  enabled: false"} {
		if !strings.Contains(spec.Values, required) {
			t.Fatalf("kube-prometheus-stack values %q do not contain %q", spec.Values, required)
		}
	}
}

func TestValidateAppPreviewBindsImmutableInputs(t *testing.T) {
	release := catalogRelease{VersionID: "version-1", Version: "1.2.3", Digest: "sha256:abc"}
	body := map[string]any{"data": map[string]any{
		"allowed": true, "application": "grafana", "artifact_digest": release.Digest,
		"catalog_digest": "sha256:catalog", "values_digest": "sha256:values",
		"checks": []any{map[string]any{"code": "catalog_trust", "status": "ready"}},
	}}
	if err := validateAppPreview(body, "grafana", release); err != nil {
		t.Fatal(err)
	}
	body["data"].(map[string]any)["artifact_digest"] = "sha256:wrong"
	if err := validateAppPreview(body, "grafana", release); err == nil {
		t.Fatal("expected mismatched artifact digest to fail")
	}
}

func TestExecuteAppMutationPollsNestedCatalogReceipt(t *testing.T) {
	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/catalog/installed/":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Fatal("missing idempotency key")
			}
			w.Header().Set("Location", "/api/v1/catalog/operations/op-1/")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"installation": map[string]any{"id": "install-1", "revision": 1},
				"operation":    map[string]any{"id": "op-1", "status": "pending"},
			}})
		case "/api/v1/catalog/operations/op-1/":
			polls++
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "op-1", "status": "completed"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	mutation, key, err := executeAppMutation(context.Background(), server.Client(), executionContext{Base: base, Token: "token", RunID: "run-1"}, http.MethodPost, "/api/v1/catalog/installed/", map[string]any{"cluster_id": "cluster-1"}, "APP-01-install")
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || mutation.InstallationID != "install-1" || mutation.Operation.ID != "op-1" || polls != 1 {
		t.Fatalf("unexpected mutation %#v key=%q polls=%d", mutation, key, polls)
	}
}

func TestCleanupAppInstallationAcceptsAlreadyAbsentRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/catalog/installed/install-1/" {
			t.Fatalf("unexpected cleanup request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("missing cleanup idempotency key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found"}})
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, key, alreadyAbsent, err := cleanupAppInstallation(context.Background(), server.Client(), executionContext{Base: base, Token: "token", RunID: "run-1"}, "install-1", "APP-01-deferred-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if key == "" || !alreadyAbsent {
		t.Fatalf("cleanup key=%q alreadyAbsent=%t, want a successful idempotent cleanup", key, alreadyAbsent)
	}
}

func TestWaitCatalogInstallationDeletedPollsToAbsence(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/catalog/installed/install-1/" {
			t.Fatalf("unexpected deletion check %s %s", r.Method, r.URL.Path)
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "install-1", "status": "uninstalling"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found"}})
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitCatalogInstallationDeleted(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, "install-1", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("deletion checks = %d, want 2", requests)
	}
}

func TestAppReleaseFootprintFindsLeakedWorkload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/deployments") {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "grafana"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	footprint, err := appReleaseFootprint(context.Background(), server.Client(), executionContext{Base: base, Token: "token"}, "cluster-1", appInstallSpec{Namespace: "monitoring", ReleaseName: "stack"})
	if err != nil {
		t.Fatal(err)
	}
	if len(footprint) != 1 || footprint[0] != "Deployment/grafana" {
		t.Fatalf("cleanup footprint = %v, want leaked workload identity", footprint)
	}
}
