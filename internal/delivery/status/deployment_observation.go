package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type deploymentInventory struct {
	protocol.DeliveryInventory
	Observation *protocol.DeliveryObservation `json:"observation,omitempty"`
}

func deploymentSourceTime(d protocol.DeliveryDeploymentStatusV2, modern bool) pgtype.Timestamptz {
	if !modern {
		return timestamp(d.ObservedAt)
	}
	if d.Observation == nil || d.Observation.ObservedAt == nil {
		return pgtype.Timestamptz{}
	}
	return timestamp(d.Observation.ObservedAt.UTC())
}

func deploymentEventTime(d protocol.DeliveryDeploymentStatusV2, modern bool, receivedAt time.Time) time.Time {
	if modern {
		return receivedAt
	}
	return d.ObservedAt
}

// A coalesced modern report can carry new source verification time without a
// semantic state transition. Keep the existing session/generation/sequence CAS,
// preserve all stored semantic fields and refresh only source metadata/sequence.
// No transition, deletion finalization, rollout, event, outbox or ack is emitted.
// The existing post-commit readiness repair remains owned by Ingest.
func refreshDeploymentSourceTimes(ctx context.Context, tx Transaction, payload protocol.DeliveryStatusV2, clusterID uuid.UUID, sessionID string) error {
	if payload.ControllerInventory.Observation == nil {
		return nil
	}
	for _, d := range payload.Deployments {
		id, _ := uuid.Parse(d.DeploymentID)
		current, err := tx.GetClusterDeploymentForDeliveryStatus(ctx, sqlc.GetClusterDeploymentForDeliveryStatusParams{ID: id, ClusterID: clusterID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("lock coalesced deployment: %w", err)
		}
		if current.DesiredGeneration != d.Generation || current.DesiredSpecDigest != d.SpecDigest || current.ObservedGeneration != d.Generation || current.ObservedSpecDigest != d.SpecDigest {
			continue
		}
		at := deploymentSourceTime(d, true)
		if at.Valid == current.LastObservedAt.Valid && (!at.Valid || at.Time.Equal(current.LastObservedAt.Time)) {
			continue
		}
		var inventory deploymentInventory
		if len(current.Inventory) > 0 {
			if err = json.Unmarshal(current.Inventory, &inventory); err != nil {
				return fmt.Errorf("decode persisted deployment inventory: %w", err)
			}
		}
		inventory.Observation = d.Observation
		raw, err := json.Marshal(inventory)
		if err != nil {
			return err
		}
		_, err = tx.UpdateClusterDeploymentObservedCAS(ctx, sqlc.UpdateClusterDeploymentObservedCASParams{
			ID: id, ObservedGeneration: d.Generation, ObservedSpecDigest: d.SpecDigest,
			ObservedRevision: current.ObservedRevision, Phase: current.Phase, Conditions: current.Conditions,
			SourceKind: current.SourceKind, SourceName: current.SourceName, ReconcilerKind: current.ReconcilerKind, ReconcilerName: current.ReconcilerName,
			Inventory: raw, AgentSessionID: sessionID, AgentSequence: payload.SessionSequence, LastErrorCode: current.LastErrorCode, LastMessage: current.LastMessage, LastObservedAt: at,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("refresh coalesced deployment source time: %w", err)
		}
	}
	return nil
}
