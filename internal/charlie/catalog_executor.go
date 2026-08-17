package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type CatalogExecutor struct {
	adapters    map[string]CapabilityExecutor
	descriptors map[string]CapabilityDescriptor
	now         func() time.Time
}

func NewCatalogExecutor(adapters map[string]CapabilityExecutor) (*CatalogExecutor, error) {
	return newCatalogExecutor(adapters, append(ReadCapabilityCatalog(), WriteCapabilityCatalog()...))
}

func newCatalogExecutor(adapters map[string]CapabilityExecutor, catalog []CapabilityDescriptor) (*CatalogExecutor, error) {
	if len(adapters) == 0 {
		return nil, fmt.Errorf("Charlie capability adapters are unavailable")
	}
	descriptors := make(map[string]CapabilityDescriptor, len(catalog))
	for _, descriptor := range catalog {
		if validateV1CapabilityDescriptor(descriptor) != nil {
			return nil, fmt.Errorf("Charlie capability catalog contains an unsafe descriptor")
		}
		if _, duplicate := descriptors[descriptor.Name]; duplicate {
			return nil, fmt.Errorf("Charlie capability catalog contains a duplicate descriptor")
		}
		descriptor.AcceptedFields = append([]string(nil), descriptor.AcceptedFields...)
		descriptors[descriptor.Name] = descriptor
	}
	copyAdapters := make(map[string]CapabilityExecutor, len(adapters))
	for name, adapter := range adapters {
		if _, ok := descriptors[name]; !ok || adapter == nil {
			return nil, fmt.Errorf("Charlie capability adapter registration is invalid")
		}
		copyAdapters[name] = adapter
	}
	return &CatalogExecutor{adapters: copyAdapters, descriptors: descriptors, now: time.Now}, nil
}

func (e *CatalogExecutor) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	adapter := e.adapters[capability.Name]
	registered, ok := e.descriptors[capability.Name]
	if adapter == nil || !ok || validateV1CapabilityDescriptor(registered) != nil || !sameCapabilityDescriptor(capability, registered) {
		return nil, fmt.Errorf("Charlie capability adapter is unavailable")
	}
	dispatch := registered
	dispatch.AcceptedFields = append([]string(nil), registered.AcceptedFields...)
	result, err := adapter.Execute(ctx, dispatch, arguments)
	if err != nil || registered.Effect != EffectRead {
		return result, err
	}
	return addCapabilityCheckedAt(result, e.now().UTC(), registered.MaxResponseBytes)
}

// addCapabilityCheckedAt makes every successful read self-dating at the
// product boundary. Adapters retain their existing response shapes and
// verification behavior; the catalog executor adds one uniform field after
// the source read completes. An adapter that already supplies a more precise
// checked_at value (for example the concurrent system health snapshot) wins.
func addCapabilityCheckedAt(result json.RawMessage, checkedAt time.Time, maxBytes int) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(result) == 0 || json.Unmarshal(result, &object) != nil || object == nil {
		return nil, fmt.Errorf("Charlie read capability result must be a JSON object")
	}
	if _, exists := object["checked_at"]; !exists {
		encoded, err := json.Marshal(checkedAt.Format(time.RFC3339Nano))
		if err != nil {
			return nil, fmt.Errorf("Charlie read capability timestamp is unavailable")
		}
		object["checked_at"] = encoded
	}
	annotated, err := json.Marshal(object)
	if err != nil || len(annotated) > maxBytes {
		return nil, fmt.Errorf("Charlie read capability result exceeds bound after timestamping")
	}
	return annotated, nil
}

func (e *CatalogExecutor) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, result json.RawMessage) (bool, error) {
	adapter := e.adapters[capability.Name]
	registered, ok := e.descriptors[capability.Name]
	if adapter == nil || !ok || validateV1CapabilityDescriptor(registered) != nil || !sameCapabilityDescriptor(capability, registered) {
		return false, fmt.Errorf("Charlie capability adapter is unavailable")
	}
	dispatch := registered
	dispatch.AcceptedFields = append([]string(nil), registered.AcceptedFields...)
	return adapter.Verify(ctx, dispatch, arguments, result)
}

// SupportsCapability keeps discovery and dispatch on the same explicit
// registration list. Missing adapters are not latent or partially available.
func (e *CatalogExecutor) SupportsCapability(_ context.Context, name string) bool {
	if e == nil {
		return false
	}
	_, ok := e.adapters[name]
	return ok
}

func MergeCapabilityAdapters(groups ...map[string]CapabilityExecutor) map[string]CapabilityExecutor {
	merged := map[string]CapabilityExecutor{}
	for _, group := range groups {
		for name, adapter := range group {
			merged[name] = adapter
		}
	}
	return merged
}

func ClusterAgentCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	adapters := map[string]CapabilityExecutor{}
	for _, capability := range ReadCapabilityCatalog() {
		if capability.Name == "astronomer.cluster_agents.summary" || capability.Name == "astronomer.cluster_agents.list" ||
			capability.Name == "astronomer.cluster_agents.get" || capability.Name == "astronomer.cluster_agents.connection_history" ||
			capability.Name == "astronomer.tunnel.health" || capability.Name == "astronomer.tunnel.replica_distribution" || capability.Name == "astronomer.tunnel.recent_errors" {
			adapters[capability.Name] = adapter
		}
	}
	return adapters
}

func ManagementKubernetesCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	adapters := map[string]CapabilityExecutor{}
	for _, name := range []string{
		"astronomer.management.workloads", "astronomer.management.workload_get",
		"astronomer.management.pods", "astronomer.management.rollout_status",
		"astronomer.management.events",
		"astronomer.management.pod_logs", "astronomer.management.nodes", "astronomer.management.storage",
		"astronomer.management.network", "astronomer.management.workload_restart",
		"astronomer.management.resource_usage", "astronomer.management.jobs", "astronomer.management.job_get",
		"astronomer.management.daemonsets", "astronomer.management.availability", "astronomer.management.ingress",
		"astronomer.management.workload_rollout", "astronomer.management.workload_scale",
		"astronomer.management.run_job", "astronomer.tunnel.restart_component",
	} {
		adapters[name] = adapter
	}
	return adapters
}
