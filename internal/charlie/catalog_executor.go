package charlie

import (
	"context"
	"encoding/json"
	"fmt"
)

type CatalogExecutor struct {
	adapters    map[string]CapabilityExecutor
	descriptors map[string]CapabilityDescriptor
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
	return &CatalogExecutor{adapters: copyAdapters, descriptors: descriptors}, nil
}

func (e *CatalogExecutor) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	adapter := e.adapters[capability.Name]
	registered, ok := e.descriptors[capability.Name]
	if adapter == nil || !ok || validateV1CapabilityDescriptor(registered) != nil || !sameCapabilityDescriptor(capability, registered) {
		return nil, fmt.Errorf("Charlie capability adapter is unavailable")
	}
	dispatch := registered
	dispatch.AcceptedFields = append([]string(nil), registered.AcceptedFields...)
	return adapter.Execute(ctx, dispatch, arguments)
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

func FleetCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	adapters := map[string]CapabilityExecutor{}
	for _, capability := range ReadCapabilityCatalog() {
		if capability.Name == "astronomer.agent_fleet.summary" || capability.Name == "astronomer.agent_fleet.list" ||
			capability.Name == "astronomer.agent_fleet.get" || capability.Name == "astronomer.agent_fleet.connection_history" ||
			capability.Name == "astronomer.agent_fleet.upgrade_status" || capability.Name == "astronomer.agent_fleet.ingestion_health" ||
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
