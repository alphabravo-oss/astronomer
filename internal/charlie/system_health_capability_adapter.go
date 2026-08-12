package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	systemHealthCapability = "astronomer.system.health"
	systemHealthCheckBytes = 8 << 10
	systemHealthWorkers    = 6
)

var systemHealthChecks = []struct {
	name      string
	arguments map[string]json.RawMessage
}{
	{name: "astronomer.installation.summary"},
	{name: "astronomer.installation.readiness"},
	{name: "astronomer.management.workloads", arguments: map[string]json.RawMessage{"page": json.RawMessage(`1`), "page_size": json.RawMessage(`25`)}},
	{name: "astronomer.management.nodes"},
	{name: "astronomer.database.health"},
	{name: "astronomer.redis.health"},
	{name: "astronomer.queue.health"},
	{name: "astronomer.agent_fleet.summary"},
	{name: "astronomer.tunnel.health"},
	{name: "astronomer.backups.status"},
	{name: "astronomer.tls.status"},
	{name: "astronomer.alerting.health"},
	{name: "astronomer.argocd.self_management_status"},
	{name: "astronomer.charlie.runtime_health"},
	{name: "astronomer.security.posture"},
}

type systemHealthCheck struct {
	Capability  string          `json:"capability"`
	Status      string          `json:"status"`
	DurationMS  int64           `json:"duration_ms"`
	FailureCode string          `json:"failure_code,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
	Omitted     bool            `json:"detail_omitted,omitempty"`
	Bytes       int             `json:"response_bytes,omitempty"`
}

// SystemHealthCapabilityAdapter composes existing product-owned reads. It
// introduces no new source access and never proxies into a downstream cluster.
type SystemHealthCapabilityAdapter struct {
	executor    *CatalogExecutor
	descriptors map[string]CapabilityDescriptor
}

func NewSystemHealthCapabilityAdapter(executor *CatalogExecutor) (*SystemHealthCapabilityAdapter, error) {
	if executor == nil {
		return nil, fmt.Errorf("Charlie system health capability is unavailable")
	}
	descriptors := make(map[string]CapabilityDescriptor, len(systemHealthChecks))
	for _, descriptor := range ReadCapabilityCatalog() {
		descriptors[descriptor.Name] = descriptor
	}
	return &SystemHealthCapabilityAdapter{executor: executor, descriptors: descriptors}, nil
}

func SystemHealthCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	return map[string]CapabilityExecutor{systemHealthCapability: adapter}
}

func (a *SystemHealthCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, _ map[string]json.RawMessage) (json.RawMessage, error) {
	if capability.Name != systemHealthCapability {
		return nil, fmt.Errorf("unsupported system health capability")
	}
	started := time.Now().UTC()
	checks := make([]systemHealthCheck, len(systemHealthChecks))
	semaphore := make(chan struct{}, systemHealthWorkers)
	var wait sync.WaitGroup
	for index, requested := range systemHealthChecks {
		index, requested := index, requested
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				checks[index] = systemHealthCheck{Capability: requested.name, Status: "unavailable", FailureCode: "aggregate_cancelled"}
				return
			}
			checkStarted := time.Now()
			result := systemHealthCheck{Capability: requested.name}
			descriptor, known := a.descriptors[requested.name]
			if !known || !a.executor.SupportsCapability(ctx, requested.name) {
				result.Status = "unavailable"
				result.FailureCode = "capability_unavailable"
				result.DurationMS = time.Since(checkStarted).Milliseconds()
				checks[index] = result
				return
			}
			value, err := a.executor.Execute(ctx, descriptor, requested.arguments)
			result.DurationMS = time.Since(checkStarted).Milliseconds()
			if err != nil {
				result.Status = "unavailable"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					result.FailureCode = "aggregate_timeout"
				} else {
					result.FailureCode = "check_failed"
				}
				checks[index] = result
				return
			}
			result.Status = "completed"
			result.Bytes = len(value)
			if len(value) <= systemHealthCheckBytes && json.Valid(value) {
				result.Data = append(json.RawMessage(nil), value...)
			} else {
				result.Omitted = true
			}
			checks[index] = result
		}()
	}
	wait.Wait()
	sort.SliceStable(checks, func(left, right int) bool { return checks[left].Capability < checks[right].Capability })

	completed, unavailable := 0, 0
	for _, check := range checks {
		if check.Status == "completed" {
			completed++
		} else {
			unavailable++
		}
	}
	response := map[string]any{
		"checked_at":  started,
		"duration_ms": time.Since(started).Milliseconds(),
		"coverage":    map[string]any{"requested": len(checks), "completed": completed, "unavailable": unavailable},
		"checks":      checks,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	// Preserve the complete coverage/status list if verbose sub-results would
	// exceed the public capability bound. Largest details are removed first.
	for len(encoded) > capability.MaxResponseBytes {
		largest := -1
		for index := range checks {
			if len(checks[index].Data) > 0 && (largest < 0 || len(checks[index].Data) > len(checks[largest].Data)) {
				largest = index
			}
		}
		if largest < 0 {
			return nil, fmt.Errorf("system health result exceeds response bound")
		}
		checks[largest].Data = nil
		checks[largest].Omitted = true
		response["checks"] = checks
		encoded, err = json.Marshal(response)
		if err != nil {
			return nil, err
		}
	}
	return encoded, nil
}

func (a *SystemHealthCapabilityAdapter) Verify(context.Context, CapabilityDescriptor, map[string]json.RawMessage, json.RawMessage) (bool, error) {
	return true, nil
}
