package delivery

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ManagedAssignmentObservationSource runs once for the runtime context. Tunnel
// connections select how snapshots are consumed, not how watches are owned.
type ManagedAssignmentObservationSource interface {
	AssignmentObservationSource
	Run(context.Context) error
}

func (r *Runtime) syncAssignmentSubscriptions() error {
	if r.config.AssignmentObservations == nil {
		return nil
	}
	accepted := make([]AcceptedAssignment, 0, len(r.checkpoint.Assignments))
	for _, id := range sortedAssignmentIDs(r.checkpoint.Assignments) {
		accepted = append(accepted, r.checkpoint.Assignments[id])
	}
	if err := r.config.AssignmentObservations.ReplaceAssignments(accepted); err != nil {
		// Never leave removed/older subscriptions active after a rejected replacement.
		_ = r.config.AssignmentObservations.ReplaceAssignments(nil)
		return fmt.Errorf("sync accepted assignment observations: %w", err)
	}
	return nil
}

func (r *Runtime) startAssignmentObservations(ctx context.Context) (func(), error) {
	if err := r.syncAssignmentSubscriptions(); err != nil {
		return nil, err
	}
	if r.config.AssignmentObservations == nil {
		return func() {}, nil
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := r.config.AssignmentObservations.Run(child); err != nil && child.Err() == nil {
			r.config.Logger.Warn("assignment observation source stopped", "error", err)
		}
	}()
	return func() { cancel(); <-done }, nil
}

func (r *Runtime) assignmentStatuses(ctx context.Context, modern bool, now time.Time) []protocol.DeliveryDeploymentStatusV2 {
	var snapshots map[string]AssignmentObservation
	if modern && r.config.AssignmentObservations != nil {
		snapshots = r.config.AssignmentObservations.SnapshotAssignments()
	}
	statuses := make([]protocol.DeliveryDeploymentStatusV2, 0, len(r.checkpoint.Assignments))
	for _, id := range sortedAssignmentIDs(r.checkpoint.Assignments) {
		accepted := r.checkpoint.Assignments[id]
		if modern {
			statuses = append(statuses, r.cachedAssignmentStatus(accepted, snapshots[id], now))
			continue
		}
		source, reconciler, err := r.observe(ctx, accepted)
		if err != nil {
			statuses = append(statuses, legacyObservationFailure(accepted, "local_observation_failed", "Flux state could not be read", now))
			continue
		}
		normalized, err := NormalizeAcceptedObservation(AcceptedObservation{Assignment: accepted, Source: source, Reconciler: reconciler, ObservedAt: now})
		if err != nil {
			normalized = legacyObservationFailure(accepted, "local_observation_refused", "Flux state failed its ownership fence", now)
		}
		statuses = append(statuses, normalized)
	}
	return statuses
}
func legacyObservationFailure(accepted AcceptedAssignment, code, message string, at time.Time) protocol.DeliveryDeploymentStatusV2 {
	return protocol.DeliveryDeploymentStatusV2{DeploymentID: accepted.DeploymentID, Generation: accepted.Generation, SpecDigest: accepted.SpecDigest, Phase: "unknown", ErrorCode: code, Message: message, ObservedAt: at}
}

func (r *Runtime) cachedAssignmentStatus(accepted AcceptedAssignment, snapshot AssignmentObservation, now time.Time) protocol.DeliveryDeploymentStatusV2 {
	observation := aggregateAssignmentObservation(snapshot.Source.Observation, snapshot.Reconciler.Observation)
	current, exists := r.checkpoint.Assignments[accepted.DeploymentID]
	if !exists || !reflect.DeepEqual(current, accepted) || !reflect.DeepEqual(snapshot.Assignment, accepted) {
		observation = protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
	}
	if observation.State == protocol.ObservationCurrent && (snapshot.Source.Object == nil || snapshot.Reconciler.Object == nil) {
		observation.State = protocol.ObservationUnavailable
	}
	if observation.State == protocol.ObservationCurrent {
		normalized, err := NormalizeAcceptedObservation(AcceptedObservation{Assignment: accepted, Source: snapshot.Source.Object, Reconciler: snapshot.Reconciler.Object, ObservedAt: *observation.ObservedAt})
		if err == nil {
			normalized.Observation = &observation
			normalized.ObservedAt = now
			return normalized
		}
		observation.State = protocol.ObservationUnavailable
	}
	status := legacyObservationFailure(accepted, "observation_"+string(observation.State), "Flux source observation is "+string(observation.State), now)
	status.Observation = &observation
	return status
}

func aggregateAssignmentObservation(source, reconciler protocol.DeliveryObservation) protocol.DeliveryObservation {
	result := protocol.DeliveryObservation{State: protocol.ObservationCurrent}
	// Fixed precedence prevents map/arrival ordering from changing diagnostics.
	rank := map[protocol.ObservationState]int{protocol.ObservationCurrent: 0, protocol.ObservationAbsent: 1, protocol.ObservationUnsynced: 2, protocol.ObservationStale: 3, protocol.ObservationDisconnected: 4, protocol.ObservationUnavailable: 5, protocol.ObservationDenied: 6}
	missingTime := false
	for _, item := range []protocol.DeliveryObservation{source, reconciler} {
		if _, ok := rank[item.State]; !ok {
			item.State = protocol.ObservationUnsynced
		}
		if rank[item.State] > rank[result.State] {
			result.State = item.State
		}
		if item.ObservedAt == nil || item.ObservedAt.IsZero() {
			missingTime = true
			continue
		}
		if result.ObservedAt == nil || item.ObservedAt.Before(*result.ObservedAt) {
			at := item.ObservedAt.UTC()
			result.ObservedAt = &at
		}
	}
	if missingTime {
		result.ObservedAt = nil
		if result.State == protocol.ObservationCurrent || result.State == protocol.ObservationAbsent || result.State == protocol.ObservationStale {
			result.State = protocol.ObservationUnsynced
		}
	}
	return result
}
