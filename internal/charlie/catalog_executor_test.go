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
	"time"
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

func TestCatalogExecutorAddsUniformCheckedAtToReadsOnly(t *testing.T) {
	executor, err := NewCatalogExecutor(map[string]CapabilityExecutor{
		"astronomer.installation.summary": staticCapabilityAdapter{},
		"astronomer.queue.retry_task":     staticCapabilityAdapter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTime := time.Date(2026, 8, 12, 5, 0, 1, 234000000, time.UTC)
	executor.now = func() time.Time { return wantTime }

	read, _ := capabilityByName("astronomer.installation.summary")
	result, err := executor.Execute(t.Context(), read, nil)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if json.Unmarshal(result, &object) != nil || object["checked_at"] != wantTime.Format(time.RFC3339Nano) || object["ok"] != true {
		t.Fatalf("read response lacks a uniform timestamp: %s", result)
	}

	write, _ := capabilityByName("astronomer.queue.retry_task")
	result, err = executor.Execute(t.Context(), write, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "checked_at") {
		t.Fatalf("write execution was mislabeled as an observation: %s", result)
	}
}

func TestCatalogExecutorPreservesAdapterCheckedAt(t *testing.T) {
	adapter := fixedCapabilityAdapter{value: json.RawMessage(`{"checked_at":"2026-08-12T04:00:00Z","healthy":true}`)}
	executor, err := NewCatalogExecutor(map[string]CapabilityExecutor{"astronomer.system.health": adapter})
	if err != nil {
		t.Fatal(err)
	}
	executor.now = func() time.Time { return time.Date(2026, 8, 12, 5, 0, 0, 0, time.UTC) }
	descriptor, _ := capabilityByName("astronomer.system.health")
	result, err := executor.Execute(t.Context(), descriptor, nil)
	if err != nil || !strings.Contains(string(result), `"checked_at":"2026-08-12T04:00:00Z"`) {
		t.Fatalf("adapter timestamp was not preserved: %s err=%v", result, err)
	}
}

type fixedCapabilityAdapter struct{ value json.RawMessage }

func (a fixedCapabilityAdapter) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	return append(json.RawMessage(nil), a.value...), nil
}

func (fixedCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
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
		ClusterAgentCapabilityAdapters(adapter), DeliveryCapabilityAdapters(adapter), ManagementKubernetesCapabilityAdapters(adapter),
		QueueCapabilityAdapters(adapter), OperationalCapabilityAdapters(adapter),
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
		"cluster_agent_capability_adapter.go", "delivery_capability_adapter.go", "management_kubernetes_adapter.go",
		"queue_capability_adapter.go",
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
