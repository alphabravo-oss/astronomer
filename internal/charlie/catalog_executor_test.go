package charlie

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type staticCapabilityAdapter struct{}

func (staticCapabilityAdapter) Execute(_ context.Context, _ CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	value := map[string]any{"ok": true}
	for _, name := range []string{"page", "page_size"} {
		if raw := arguments[name]; len(raw) > 0 {
			var number int64
			if json.Unmarshal(raw, &number) == nil {
				value[name] = number
			}
		}
	}
	return json.Marshal(value)
}
func (staticCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func TestCatalogExecutorDiscoveryIsExactlyRegisteredCapabilities(t *testing.T) {
	executor, err := NewCatalogExecutor(map[string]CapabilityExecutor{"astronomer.installation.summary": staticCapabilityAdapter{}})
	if err != nil {
		t.Fatal(err)
	}
	tools := mcpToolsFor(context.Background(), executor)
	if len(tools) != 1 || tools[0]["name"] != "astronomer.installation.summary" {
		t.Fatalf("tools=%v", tools)
	}
	if executor.SupportsCapability(context.Background(), "astronomer.management.run_job") {
		t.Fatal("unregistered write capability was supported")
	}
}

func TestCapabilityDisclosureDigestIsDeterministicAndCatalogBound(t *testing.T) {
	first := CapabilityDisclosureDigest()
	second := CapabilityDisclosureDigest()
	if first == "" || first != second {
		t.Fatalf("digest is not deterministic: %q != %q", first, second)
	}
	executor, _ := NewCatalogExecutor(map[string]CapabilityExecutor{"astronomer.installation.summary": staticCapabilityAdapter{}})
	narrow := capabilityDisclosureDigest(mcpToolsFor(context.Background(), executor))
	if narrow == "" || narrow == first {
		t.Fatalf("narrow disclosure digest did not change: full=%s narrow=%s", first, narrow)
	}
}

func TestCatalogRejectsUnknownAndNilAdapterRegistrations(t *testing.T) {
	for _, adapters := range []map[string]CapabilityExecutor{
		{"astronomer.downstream.pods": staticCapabilityAdapter{}},
		{"astronomer.installation.summary": nil},
	} {
		if _, err := NewCatalogExecutor(adapters); err == nil {
			t.Fatalf("invalid registration accepted: %#v", adapters)
		}
	}
}

func TestProductionAdapterGroupsCoverTheEntireV1Catalog(t *testing.T) {
	adapter := staticCapabilityAdapter{}
	registrations := MergeCapabilityAdapters(
		FleetCapabilityAdapters(adapter), ManagementKubernetesCapabilityAdapters(adapter),
		QueueCapabilityAdapters(adapter), ArgoCDCapabilityAdapters(adapter), OperationalCapabilityAdapters(adapter),
		WorkPipelineCapabilityAdapters(adapter), RuntimeCapabilityAdapters(adapter), AdminVisibilityCapabilityAdapters(adapter),
		SystemHealthCapabilityAdapters(adapter),
	)
	catalog := append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...)
	if len(registrations) != len(catalog) {
		t.Fatalf("registrations=%d catalog=%d", len(registrations), len(catalog))
	}
	for _, capability := range catalog {
		if registrations[capability.Name] == nil {
			t.Fatalf("catalog capability %s has no production adapter group", capability.Name)
		}
	}
}

func TestProductionCapabilityAdaptersCannotCallDownstreamTunnel(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate adapter sources")
	}
	directory := filepath.Dir(currentFile)
	for _, name := range []string{
		"fleet_capability_adapter.go", "management_kubernetes_adapter.go",
		"queue_capability_adapter.go", "argocd_capability_adapter.go",
		"operational_capability_adapter.go", "work_pipeline_capability_adapter.go",
		"runtime_capability_adapter.go",
		"admin_visibility_capability_adapter.go",
		"system_health_capability_adapter.go",
	} {
		name := name
		t.Run(name, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imported := range parsed.Imports {
				path, err := strconv.Unquote(imported.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				for _, forbidden := range []string{"/internal/tunnel", "/internal/agent", "remotedialer"} {
					if strings.Contains(path, forbidden) {
						t.Fatalf("production capability adapter imports downstream transport %q", path)
					}
				}
			}

			parsed, err = parser.ParseFile(token.NewFileSet(), filepath.Join(directory, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				for _, forbidden := range []string{"K8sProxy", "ServiceProxyRequest", "TunnelClient", "SendHelmRequest"} {
					if identifier.Name == forbidden {
						t.Errorf("production capability adapter references downstream transport identifier %q", forbidden)
					}
				}
				return true
			})
		})
	}
}
