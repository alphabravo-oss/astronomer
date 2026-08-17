// Package status persists normalized Flux observations from authenticated
// downstream agent sessions.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/compatibility"
	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var (
	ErrClusterIdentityMismatch = errors.New("delivery status cluster does not match authenticated session")
	ErrSessionSuperseded       = errors.New("delivery status agent session is no longer active")
)

type Transaction interface {
	FenceDeliveryAgentSession(context.Context, sqlc.FenceDeliveryAgentSessionParams) (uuid.UUID, error)
	GetClusterDeploymentForDeliveryStatus(context.Context, sqlc.GetClusterDeploymentForDeliveryStatusParams) (sqlc.ClusterDeployment, error)
	UpdateClusterDeploymentObservedCAS(context.Context, sqlc.UpdateClusterDeploymentObservedCASParams) (sqlc.ClusterDeployment, error)
	AdvanceDeliveryRolloutClusterFromStatus(context.Context, sqlc.AdvanceDeliveryRolloutClusterFromStatusParams) (sqlc.AdvanceDeliveryRolloutClusterFromStatusRow, error)
	CreateClusterDeploymentEvent(context.Context, sqlc.CreateClusterDeploymentEventParams) (sqlc.ClusterDeploymentEvent, error)
	CreateDeliveryRolloutEvent(context.Context, sqlc.CreateDeliveryRolloutEventParams) (sqlc.DeliveryRolloutEvent, error)
	UpsertTaskOutbox(context.Context, sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error)
	UpsertDeliveryControllerInventory(context.Context, sqlc.UpsertDeliveryControllerInventoryParams) (sqlc.DeliveryControllerInventory, error)
	FinalizeDeliveryTargetDeletionIfComplete(context.Context, uuid.UUID) (sqlc.DeliveryTarget, error)
	AcknowledgeDeliveryAssignmentSnapshot(context.Context, sqlc.AcknowledgeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error)
}

type systemInventoryTransaction interface {
	ObserveDeliverySystemAssignment(context.Context, sqlc.ObserveDeliverySystemAssignmentParams) (sqlc.ObserveDeliverySystemAssignmentRow, error)
	CreateDeliverySystemEvent(context.Context, sqlc.CreateDeliverySystemEventParams) (sqlc.DeliverySystemEvent, error)
}

type Runner interface {
	Run(context.Context, func(Transaction) error) error
}

type SQLRunner struct{ pool *pgxpool.Pool }

func NewSQLRunner(pool *pgxpool.Pool) *SQLRunner { return &SQLRunner{pool: pool} }

func (r *SQLRunner) Run(ctx context.Context, work func(Transaction) error) error {
	if r == nil || r.pool == nil {
		return errors.New("delivery status transaction runner is not configured")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := work(sqlc.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReadyReconciler is invoked only after an authenticated status transaction
// commits a Ready, compatible Flux inventory. Its failure is retryable side
// work and never rolls back or rejects the agent's observation.
type ReadyReconciler interface {
	Reconcile(context.Context, uuid.UUID) error
}

type Ingester struct {
	runner Runner
	ready  ReadyReconciler
}

func NewIngester(runner Runner) *Ingester { return &Ingester{runner: runner} }

func (i *Ingester) SetReadyReconciler(reconciler ReadyReconciler) {
	if i != nil {
		i.ready = reconciler
	}
}

// Ingest binds payload to the current, database-backed tunnel connection and
// commits inventory, all non-stale deployment updates, transition events, and
// the snapshot acknowledgment atomically.
func (i *Ingester) Ingest(ctx context.Context, authenticatedCluster, connectionID uuid.UUID, sessionID string, payload protocol.DeliveryStatusV2) (finalErr error) {
	defer func() {
		result := "accepted"
		switch {
		case errors.Is(finalErr, ErrClusterIdentityMismatch):
			result = "invalid"
		case errors.Is(finalErr, ErrSessionSuperseded):
			result = "replay_rejected"
		case finalErr != nil:
			result = "failure"
		}
		deliverymetrics.ObserveStatus(result)
		deliverymetrics.ObserveProtocol("agent_to_server", result)
	}()
	if i == nil || i.runner == nil {
		return errors.New("delivery status ingester is not configured")
	}
	if authenticatedCluster == uuid.Nil || connectionID == uuid.Nil || strings.TrimSpace(sessionID) == "" {
		return errors.New("authenticated delivery session identity is incomplete")
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("validate delivery status: %w", err)
	}
	if payload.ClusterID != authenticatedCluster.String() {
		return ErrClusterIdentityMismatch
	}
	acceptedPhases := make([]string, 0, len(payload.Deployments))
	statusResults := make([]string, 0, len(payload.Deployments))
	finalErr = i.runner.Run(ctx, func(tx Transaction) error {
		if _, err := tx.FenceDeliveryAgentSession(ctx, sqlc.FenceDeliveryAgentSessionParams{
			ConnectionID: connectionID, ClusterID: authenticatedCluster, SessionID: sessionID,
		}); errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionSuperseded
		} else if err != nil {
			return fmt.Errorf("fence delivery agent session: %w", err)
		}

		components, _ := json.Marshal(payload.ControllerInventory.Components)
		apiVersions, _ := json.Marshal(payload.ControllerInventory.APIVersions)
		compatibilityResult := compatibility.Evaluate(payload.ControllerInventory)
		if _, err := tx.UpsertDeliveryControllerInventory(ctx, sqlc.UpsertDeliveryControllerInventoryParams{
			ClusterID: authenticatedCluster, AgentVersion: payload.ControllerInventory.AgentVersion,
			FluxVersion: payload.ControllerInventory.FluxVersion,
			Components:  components, ApiVersions: apiVersions,
			DistributionDigest: payload.ControllerInventory.DistributionDigest,
			KubernetesVersion:  payload.ControllerInventory.KubernetesVersion,
			Ready:              payload.ControllerInventory.Ready, CompatibilityStatus: string(compatibilityResult.Status),
			ErrorCode: compatibilityResult.Code, ObservedAt: timestamp(time.Now().UTC()),
		}); err != nil {
			return fmt.Errorf("persist delivery controller inventory: %w", err)
		}
		if systemTx, ok := tx.(systemInventoryTransaction); ok {
			observedAt := time.Now().UTC()
			observed, err := systemTx.ObserveDeliverySystemAssignment(ctx, sqlc.ObserveDeliverySystemAssignmentParams{
				ClusterID: authenticatedCluster, ObservedDistributionDigest: payload.ControllerInventory.DistributionDigest,
				ObservedAgentVersion: payload.ControllerInventory.AgentVersion, ObservedAt: timestamp(observedAt),
				InventoryReady: payload.ControllerInventory.Ready, CompatibilityStatus: string(compatibilityResult.Status),
				ErrorCode: compatibilityResult.Code,
			})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("persist delivery system observation: %w", err)
			}
			if err == nil && observed.PreviousPhase != observed.Phase {
				decisionDigest, digestErr := model.CanonicalDigest(struct {
					ClusterID  uuid.UUID `json:"cluster_id"`
					ReleaseID  uuid.UUID `json:"release_id"`
					Generation int64     `json:"generation"`
					FromPhase  string    `json:"from_phase"`
					ToPhase    string    `json:"to_phase"`
					ObservedAt time.Time `json:"observed_at"`
				}{observed.ClusterID, observed.DesiredReleaseID, observed.Generation, observed.PreviousPhase, observed.Phase, observedAt})
				if digestErr != nil {
					return fmt.Errorf("digest delivery system observation: %w", digestErr)
				}
				if _, err := systemTx.CreateDeliverySystemEvent(ctx, sqlc.CreateDeliverySystemEventParams{
					RolloutID: observed.RolloutID, ClusterID: pgtype.UUID{Bytes: authenticatedCluster, Valid: true},
					ReleaseID: observed.DesiredReleaseID, Generation: observed.Generation,
					EventType: "inventory_transition", FromPhase: observed.PreviousPhase, ToPhase: observed.Phase,
					ReasonCode: observed.LastErrorCode, DecisionDigest: decisionDigest.String(), OccurredAt: observedAt,
				}); err != nil {
					return fmt.Errorf("append delivery system event: %w", err)
				}
			}
		}

		for index := range payload.Deployments {
			observation := payload.Deployments[index]
			deploymentID, _ := uuid.Parse(observation.DeploymentID)
			current, err := tx.GetClusterDeploymentForDeliveryStatus(ctx, sqlc.GetClusterDeploymentForDeliveryStatusParams{
				ID: deploymentID, ClusterID: authenticatedCluster,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				statusResults = append(statusResults, "dropped")
				continue // stale status for a deployment already finalized and removed
			}
			if err != nil {
				return fmt.Errorf("lock cluster deployment %s: %w", observation.DeploymentID, err)
			}
			if current.DesiredGeneration != observation.Generation || current.DesiredSpecDigest != observation.SpecDigest {
				statusResults = append(statusResults, "stale_rejected")
				continue
			}
			conditions := sanitizeConditions(observation.Conditions)
			conditionsJSON, _ := json.Marshal(conditions)
			inventoryJSON, _ := json.Marshal(observation.Inventory)
			errorCode := observation.ErrorCode
			if errorCode == "" && len(observation.WarningCodes) != 0 {
				errorCode = observation.WarningCodes[0]
			}
			message := redaction.String(observation.Message)
			updated, err := tx.UpdateClusterDeploymentObservedCAS(ctx, sqlc.UpdateClusterDeploymentObservedCASParams{
				ObservedGeneration: observation.Generation, ObservedSpecDigest: observation.SpecDigest,
				ObservedRevision: observation.ObservedRevision, Phase: observation.Phase,
				Conditions: conditionsJSON, SourceKind: observation.SourceKind, SourceName: observation.SourceName,
				ReconcilerKind: observation.ReconcilerKind, ReconcilerName: observation.ReconcilerName,
				Inventory: inventoryJSON, AgentSessionID: sessionID, AgentSequence: payload.SessionSequence,
				LastErrorCode: errorCode, LastMessage: message, LastObservedAt: timestamp(observation.ObservedAt), ID: deploymentID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				statusResults = append(statusResults, "replay_rejected")
				continue // duplicate/out-of-order sequence; idempotent no-op
			}
			if err != nil {
				return fmt.Errorf("persist cluster deployment %s: %w", observation.DeploymentID, err)
			}
			acceptedPhases = append(acceptedPhases, updated.Phase)
			if current.Phase != updated.Phase || current.LastErrorCode != updated.LastErrorCode {
				if _, err := tx.CreateClusterDeploymentEvent(ctx, sqlc.CreateClusterDeploymentEventParams{
					DeploymentID: deploymentID, RolloutID: current.CurrentRolloutID,
					EventType: "status_transition", FromPhase: current.Phase, ToPhase: updated.Phase,
					Generation: observation.Generation, SpecDigest: observation.SpecDigest,
					ReasonCode: errorCode, Message: message, ObservedAt: observation.ObservedAt,
				}); err != nil {
					return fmt.Errorf("persist cluster deployment event %s: %w", observation.DeploymentID, err)
				}
			}
			if updated.Phase == "removed" {
				if _, finalizeErr := tx.FinalizeDeliveryTargetDeletionIfComplete(ctx, updated.TargetID); finalizeErr != nil && !errors.Is(finalizeErr, pgx.ErrNoRows) {
					return fmt.Errorf("finalize delivery target deletion %s: %w", updated.TargetID, finalizeErr)
				}
			}
			advanced, err := tx.AdvanceDeliveryRolloutClusterFromStatus(ctx, sqlc.AdvanceDeliveryRolloutClusterFromStatusParams{
				Phase: updated.Phase, LastErrorCode: updated.LastErrorCode, DeploymentID: deploymentID,
				ObservedGeneration: updated.ObservedGeneration, ObservedSpecDigest: updated.ObservedSpecDigest,
			})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("advance rollout cluster for deployment %s: %w", observation.DeploymentID, err)
			}
			if err == nil {
				decisionDigest, digestErr := model.CanonicalDigest(struct {
					RolloutID       uuid.UUID `json:"rollout_id"`
					ClusterID       uuid.UUID `json:"cluster_id"`
					DeploymentID    uuid.UUID `json:"deployment_id"`
					Generation      int64     `json:"generation"`
					SpecDigest      string    `json:"spec_digest"`
					Phase           string    `json:"phase"`
					SessionSequence int64     `json:"session_sequence"`
				}{advanced.RolloutID, advanced.ClusterID, deploymentID, updated.ObservedGeneration, updated.ObservedSpecDigest, updated.Phase, payload.SessionSequence})
				if digestErr != nil {
					return fmt.Errorf("digest rollout status advance: %w", digestErr)
				}
				if _, err := tx.CreateDeliveryRolloutEvent(ctx, sqlc.CreateDeliveryRolloutEventParams{
					RolloutID: advanced.RolloutID, ClusterID: pgtype.UUID{Bytes: advanced.ClusterID, Valid: true},
					DecisionDigest: decisionDigest.String(), EventType: "status_advance",
					FromState: advanced.FromState, ToState: advanced.State,
					ReasonCode: updated.LastErrorCode, Fence: advanced.Fence, OccurredAt: observation.ObservedAt,
				}); err != nil {
					return fmt.Errorf("append rollout status event: %w", err)
				}
				wakePayload, _ := json.Marshal(struct {
					RolloutID uuid.UUID `json:"rollout_id"`
				}{advanced.RolloutID})
				if _, err := tx.UpsertTaskOutbox(ctx, sqlc.UpsertTaskOutboxParams{
					DedupeKey: pgtype.Text{String: "delivery-rollout-status:" + decisionDigest.String(), Valid: true},
					TaskType:  rollout.TaskType, Payload: wakePayload, QueueName: "default", MaxRetry: 12,
					TimeoutSeconds: 120, UniqueSeconds: 1, MaxDeliveryAttempts: 20,
					NextAttemptAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				}); err != nil {
					return fmt.Errorf("enqueue rollout status wake: %w", err)
				}
			}
		}
		if payload.SnapshotGeneration > 0 {
			if _, err := tx.AcknowledgeDeliveryAssignmentSnapshot(ctx, sqlc.AcknowledgeDeliveryAssignmentSnapshotParams{
				SnapshotGeneration: payload.SnapshotGeneration, SnapshotEtag: payload.SnapshotETag,
				AgentSessionID: sessionID, AgentSequence: payload.SessionSequence,
				LastProtocolErrorCode: "", ClusterID: authenticatedCluster,
			}); err != nil {
				return fmt.Errorf("acknowledge delivery snapshot: %w", err)
			}
		}
		return nil
	})
	if finalErr == nil {
		for _, result := range statusResults {
			deliverymetrics.ObserveStatus(result)
		}
		for _, phase := range acceptedPhases {
			deliverymetrics.ObserveDeployment(phase, false)
		}
		if i.ready != nil && payload.ControllerInventory.Ready && compatibility.Evaluate(payload.ControllerInventory).Status == compatibility.Compatible {
			// The next periodic status retries failures. Status itself is already
			// durably accepted and must not be converted into an agent protocol
			// failure by baseline provisioning.
			_ = i.ready.Reconcile(ctx, authenticatedCluster)
		}
	}
	return finalErr
}

func sanitizeConditions(input []protocol.DeliveryCondition) []protocol.DeliveryCondition {
	result := append([]protocol.DeliveryCondition(nil), input...)
	for index := range result {
		result[index].Message = redaction.String(result[index].Message)
	}
	return result
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
