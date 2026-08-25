package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type kubernetesSchemaVersionFixture struct {
	KubernetesVersion string   `json:"kubernetes_version"`
	SourceURL         string   `json:"source_url"`
	RootProperties    []string `json:"root_properties"`
	SpecProperties    []string `json:"spec_properties"`
	StatusProperties  []string `json:"status_properties"`
}

type versionedResourceDiscoveryRequester struct {
	fixture kubernetesSchemaVersionFixture
}

func schemaProperties(names []string) map[string]any {
	properties := make(map[string]any, len(names))
	for _, name := range names {
		properties[name] = map[string]any{"type": "string"}
	}
	return properties
}

func (v versionedResourceDiscoveryRequester) Do(_ context.Context, _ string, _ string, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	documentPath := "/openapi/v3/apis/apps/v1?hash=" + v.fixture.KubernetesVersion
	rootProperties := schemaProperties(v.fixture.RootProperties)
	rootProperties["spec"] = map[string]any{"$ref": "#/components/schemas/io.k8s.api.apps.v1.DeploymentSpec"}
	rootProperties["status"] = map[string]any{"$ref": "#/components/schemas/io.k8s.api.apps.v1.DeploymentStatus"}
	responses := map[string]any{
		"/apis/apps/v1": map[string]any{"resources": []any{map[string]any{
			"name": "deployments", "kind": "Deployment", "namespaced": true,
			"verbs": []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		}}},
		"/openapi/v3": map[string]any{"paths": map[string]any{
			"apis/apps/v1": map[string]any{"serverRelativeURL": documentPath},
		}},
		documentPath: map[string]any{"components": map[string]any{"schemas": map[string]any{
			"io.k8s.api.apps.v1.Deployment": map[string]any{
				"type": "object", "properties": rootProperties,
				"x-kubernetes-group-version-kind": []any{map[string]any{"group": "apps", "version": "v1", "kind": "Deployment"}},
			},
			"io.k8s.api.apps.v1.DeploymentSpec": map[string]any{
				"type": "object", "properties": schemaProperties(v.fixture.SpecProperties),
			},
			"io.k8s.api.apps.v1.DeploymentStatus": map[string]any{
				"type": "object", "properties": schemaProperties(v.fixture.StatusProperties),
			},
		}}},
	}
	payload, ok := responses[path]
	if !ok {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte(`{"message":"not found"}`))}, nil
	}
	body, _ := json.Marshal(payload)
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(body)}, nil
}

func loadKubernetesSchemaVersionFixtures(t *testing.T) []kubernetesSchemaVersionFixture {
	t.Helper()
	path := filepath.Join("testdata", "kubernetes-schema-versions.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []kubernetesSchemaVersionFixture
	if err := json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

type resourceDiscoveryRequester struct{}

func (resourceDiscoveryRequester) Do(_ context.Context, _ string, _ string, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	responses := map[string]any{
		"/apis/apps/v1": map[string]any{"resources": []any{map[string]any{
			"name": "deployments", "kind": "Deployment", "namespaced": true,
			"verbs":      []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			"shortNames": []string{"deploy"}, "categories": []string{"all"},
		}}},
		"/openapi/v3": map[string]any{"paths": map[string]any{
			"apis/apps/v1": map[string]any{"serverRelativeURL": "/openapi/v3/apis/apps/v1?hash=fixture"},
		}},
		"/openapi/v3/apis/apps/v1?hash=fixture": map[string]any{"components": map[string]any{"schemas": map[string]any{
			"io.k8s.api.apps.v1.Deployment": map[string]any{
				"type":                            "object",
				"x-kubernetes-group-version-kind": []any{map[string]any{"group": "apps", "version": "v1", "kind": "Deployment"}},
				"properties": map[string]any{"spec": map[string]any{
					"$ref": "#/components/schemas/io.k8s.api.apps.v1.DeploymentSpec",
				}},
			},
			"io.k8s.api.apps.v1.DeploymentSpec": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"replicas": map[string]any{"type": "integer", "default": float64(1)},
					"strategy": map[string]any{"$ref": "#/components/schemas/io.k8s.api.apps.v1.DeploymentStrategy"},
				},
			},
			"io.k8s.api.apps.v1.DeploymentStrategy": map[string]any{
				"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string"}},
			},
		}}},
	}
	payload, ok := responses[path]
	if !ok {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound, Body: base64.StdEncoding.EncodeToString([]byte(`{"message":"not found"}`))}, nil
	}
	body, _ := json.Marshal(payload)
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString(body)}, nil
}

func TestGetResourceSchemaCombinesDiscoveryAndOpenAPI(t *testing.T) {
	h := NewResourceHandlerWithRequester(resourceDiscoveryRequester{})
	clusterID := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/resources/schema/?resource_type=deployments", nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("cluster_id", clusterID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	rec := httptest.NewRecorder()
	h.GetResourceSchema(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data struct {
			SchemaAvailable      bool                   `json:"schema_available"`
			SchemaName           string                 `json:"schema_name"`
			Definitions          map[string]any         `json:"definitions"`
			DefinitionsTruncated bool                   `json:"definitions_truncated"`
			Resource             resourceDiscoveryEntry `json:"resource"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.SchemaAvailable || envelope.Data.SchemaName != "io.k8s.api.apps.v1.Deployment" {
		t.Fatalf("schema response = %+v", envelope.Data)
	}
	if envelope.Data.Resource.Kind != "Deployment" || !envelope.Data.Resource.Namespaced {
		t.Fatalf("resource discovery = %+v", envelope.Data.Resource)
	}
	if envelope.Data.DefinitionsTruncated || len(envelope.Data.Definitions) != 2 {
		t.Fatalf("referenced definitions = %+v, truncated=%t", envelope.Data.Definitions, envelope.Data.DefinitionsTruncated)
	}
	if _, ok := envelope.Data.Definitions["io.k8s.api.apps.v1.DeploymentStrategy"]; !ok {
		t.Fatalf("transitive strategy definition missing: %+v", envelope.Data.Definitions)
	}
}

func TestGetResourceSchemaAcrossSupportedKubernetesVersions(t *testing.T) {
	fixtures := loadKubernetesSchemaVersionFixtures(t)
	if len(fixtures) != 3 {
		t.Fatalf("version fixtures = %d, want 3", len(fixtures))
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.KubernetesVersion, func(t *testing.T) {
			if fixture.SourceURL == "" {
				t.Fatal("fixture must retain its upstream source URL")
			}
			h := NewResourceHandlerWithRequester(versionedResourceDiscoveryRequester{fixture: fixture})
			clusterID := uuid.NewString()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+clusterID+"/resources/schema/?resource_type=deployments", nil)
			route := chi.NewRouteContext()
			route.URLParams.Add("cluster_id", clusterID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
			rec := httptest.NewRecorder()
			h.GetResourceSchema(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Data struct {
					SchemaAvailable bool                   `json:"schema_available"`
					Schema          map[string]any         `json:"schema"`
					Definitions     map[string]any         `json:"definitions"`
					Resource        resourceDiscoveryEntry `json:"resource"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if !envelope.Data.SchemaAvailable || envelope.Data.Resource.Kind != "Deployment" {
				t.Fatalf("schema response = %+v", envelope.Data)
			}
			properties, _ := envelope.Data.Schema["properties"].(map[string]any)
			actualRoot := make([]string, 0, len(properties))
			for name := range properties {
				actualRoot = append(actualRoot, name)
			}
			slices.Sort(actualRoot)
			expectedRoot := slices.Clone(fixture.RootProperties)
			slices.Sort(expectedRoot)
			if !slices.Equal(actualRoot, expectedRoot) {
				t.Fatalf("root properties = %v, want %v", actualRoot, expectedRoot)
			}
			for _, schemaName := range []string{
				"io.k8s.api.apps.v1.DeploymentSpec",
				"io.k8s.api.apps.v1.DeploymentStatus",
			} {
				if _, ok := envelope.Data.Definitions[schemaName]; !ok {
					t.Fatalf("referenced definition %q missing from %v", schemaName, envelope.Data.Definitions)
				}
			}
		})
	}
}

func TestReferencedResourceSchemasIsBoundedAndCycleSafe(t *testing.T) {
	document := map[string]any{"components": map[string]any{"schemas": map[string]any{
		"One":   map[string]any{"$ref": "#/components/schemas/Two"},
		"Two":   map[string]any{"$ref": "#/components/schemas/One"},
		"Three": map[string]any{"type": "string"},
	}}}
	root := map[string]any{
		"properties": map[string]any{
			"one":   map[string]any{"$ref": "#/components/schemas/One"},
			"three": map[string]any{"$ref": "#/components/schemas/Three"},
		},
	}
	definitions, truncated := referencedResourceSchemas(document, root, 2)
	if !truncated || len(definitions) != 2 {
		t.Fatalf("definitions=%+v truncated=%t, want a bounded two-entry closure", definitions, truncated)
	}
}

func TestSummarizeDiscoveredCRDsIncludesPrinterColumns(t *testing.T) {
	payload := map[string]any{"items": []any{map[string]any{
		"metadata": map[string]any{"name": "widgets.example.io"},
		"spec": map[string]any{
			"group": "example.io", "scope": "Namespaced",
			"names": map[string]any{"kind": "Widget", "plural": "widgets", "shortNames": []any{"wdg"}},
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true,
				"additionalPrinterColumns": []any{map[string]any{"name": "Ready", "jsonPath": ".status.ready"}},
			}},
		},
	}}}
	rows := summarizeDiscoveredCRDs(payload)
	if len(rows) != 1 || rows[0]["kind"] != "Widget" {
		t.Fatalf("CRD summaries = %+v", rows)
	}
	versions, _ := rows[0]["versions"].([]map[string]any)
	if len(versions) != 1 || versions[0]["printer_columns"] == nil {
		t.Fatalf("CRD versions = %+v", versions)
	}
}
