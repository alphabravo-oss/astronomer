package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
		spec := appSpec(slug, memberTarget{Namespace: "qualification"})
		if spec.Namespace == "" || spec.ReleaseName == "" {
			t.Fatalf("%s has incomplete install spec: %#v", slug, spec)
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
