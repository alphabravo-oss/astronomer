package agent

import (
	"k8s.io/client-go/dynamic"

	"github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/agent/observation"
)

// NewAssignmentObservationCache wires the five fixed Flux kinds into the same
// private telemetry path as shared typed observations. Runtime owns its lifetime.
func NewAssignmentObservationCache(client dynamic.Interface) (*delivery.AssignmentCache, error) {
	return delivery.NewAssignmentCache(client, delivery.AssignmentCacheOptions{Observation: observation.Options{RecordRequest: recordObservationRequest, RecordWatchEvent: recordObservationWatchEvent}})
}

// NewObservedDeliveryRuntime keeps standalone and embedded composition on the
// same executor/cache credentials and telemetry wiring. Runtime owns the cache.
func NewObservedDeliveryRuntime(config delivery.RuntimeConfig, client dynamic.Interface, store delivery.CheckpointStore, probe delivery.CapabilityProbe) (*delivery.Runtime, error) {
	executor, err := delivery.NewExecutor(client)
	if err != nil {
		return nil, err
	}
	source, err := NewAssignmentObservationCache(client)
	if err != nil {
		return nil, err
	}
	config.AssignmentObservations = source
	return delivery.NewRuntime(config, executor, store, probe)
}
