package charlie

import (
	"context"
	"encoding/json"
	"testing"
)

type systemHealthFixtureExecutor struct {
	value json.RawMessage
	err   error
}

func (f systemHealthFixtureExecutor) Execute(context.Context, CapabilityDescriptor, map[string]json.RawMessage) (json.RawMessage, error) {
	return append(json.RawMessage(nil), f.value...), f.err
}

func (systemHealthFixtureExecutor) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}

func TestSystemHealthCapabilityRunsBoundedAvailableChecksAndPreservesCoverage(t *testing.T) {
	fixture := systemHealthFixtureExecutor{value: json.RawMessage(`{"healthy":true}`)}
	base, err := NewCatalogExecutor(map[string]CapabilityExecutor{
		"astronomer.installation.summary":   fixture,
		"astronomer.installation.readiness": fixture,
		"astronomer.queue.health":           fixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewSystemHealthCapabilityAdapter(base)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := requireCapabilityDescriptor(t, systemHealthCapability)
	result, err := adapter.Execute(t.Context(), descriptor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > descriptor.MaxResponseBytes {
		t.Fatalf("aggregate response exceeded bound: %d", len(result))
	}
	var decoded struct {
		Coverage struct {
			Requested   int `json:"requested"`
			Completed   int `json:"completed"`
			Unavailable int `json:"unavailable"`
		} `json:"coverage"`
		Checks []systemHealthCheck `json:"checks"`
	}
	if err = json.Unmarshal(result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Coverage.Requested != len(systemHealthChecks) || decoded.Coverage.Completed != 3 ||
		decoded.Coverage.Unavailable != len(systemHealthChecks)-3 || len(decoded.Checks) != len(systemHealthChecks) {
		t.Fatalf("unexpected aggregate coverage: %#v", decoded)
	}
	for index := 1; index < len(decoded.Checks); index++ {
		if decoded.Checks[index-1].Capability > decoded.Checks[index].Capability {
			t.Fatalf("checks are not deterministic: %#v", decoded.Checks)
		}
	}
}

func requireCapabilityDescriptor(t *testing.T, name string) CapabilityDescriptor {
	t.Helper()
	for _, descriptor := range ReadCapabilityCatalog() {
		if descriptor.Name == name {
			return descriptor
		}
	}
	t.Fatalf("capability %s not found", name)
	return CapabilityDescriptor{}
}
