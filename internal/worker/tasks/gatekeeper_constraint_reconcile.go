package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/gatekeeperpolicy"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

const (
	GatekeeperConstraintReconcileType    = "gatekeeper_constraint:reconcile"
	GatekeeperConstraintReconcileAllType = "gatekeeper_constraint:reconcile_all"
	gatekeeperConstraintFieldManager     = "astronomer-authored-constraint"
	gatekeeperConstraintRecoveryBatch    = 200
	gatekeeperConstraintTimeout          = 30 * time.Second
)

type GatekeeperConstraintReconcilePayload struct {
	ClusterID  string `json:"cluster_id"`
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
}

func NewGatekeeperConstraintReconcileTask(clusterID uuid.UUID, name string, generation int64) (*asynq.Task, error) {
	if clusterID == uuid.Nil || name == "" || generation <= 0 {
		return nil, errors.New("gatekeeper constraint reconcile requires cluster_id, name, and generation")
	}
	payload, err := json.Marshal(GatekeeperConstraintReconcilePayload{
		ClusterID: clusterID.String(), Name: name, Generation: generation,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Gatekeeper constraint reconcile: %w", err)
	}
	return asynq.NewTask(GatekeeperConstraintReconcileType, payload, asynq.MaxRetry(5)), nil
}

func NewGatekeeperConstraintReconcileAllTask() *asynq.Task {
	return asynq.NewTask(GatekeeperConstraintReconcileAllType, nil, asynq.MaxRetry(2))
}

type gatekeeperConstraintReconcileQuerier interface {
	GetAuthoredConstraintByName(context.Context, sqlc.GetAuthoredConstraintByNameParams) (sqlc.AuthoredConstraint, error)
	ListRecoverableAuthoredConstraints(context.Context, int32) ([]sqlc.AuthoredConstraint, error)
	MarkAuthoredConstraintReconcileResult(context.Context, sqlc.MarkAuthoredConstraintReconcileResultParams) (int64, error)
}

func HandleGatekeeperConstraintReconcile(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return asynq.SkipRetry
	}
	var payload GatekeeperConstraintReconcilePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode Gatekeeper constraint reconcile: %w: %w", err, asynq.SkipRetry)
	}
	clusterID, err := uuid.Parse(payload.ClusterID)
	if err != nil || payload.Name == "" || payload.Generation <= 0 {
		return fmt.Errorf("invalid Gatekeeper constraint reconcile payload: %w", asynq.SkipRetry)
	}
	return reconcileGatekeeperConstraint(ctx, clusterID, payload.Name, payload.Generation)
}

func HandleGatekeeperConstraintReconcileAll(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, GatekeeperConstraintReconcileAllType, func() error {
		q, err := gatekeeperConstraintQueries(ctx)
		if err != nil {
			return err
		}
		rows, err := q.ListRecoverableAuthoredConstraints(ctx, gatekeeperConstraintRecoveryBatch)
		if err != nil {
			return fmt.Errorf("list recoverable Gatekeeper constraints: %w", err)
		}
		var batchErr error
		for _, row := range rows {
			rowCtx, cancel := context.WithTimeout(ctx, gatekeeperConstraintTimeout)
			err := reconcileGatekeeperConstraint(rowCtx, row.ClusterID, row.Name, row.Generation)
			cancel()
			if err != nil {
				batchErr = errors.Join(batchErr, fmt.Errorf("%s/%s: %w", row.ClusterID, row.Name, err))
			}
		}
		return batchErr
	})
}

func reconcileGatekeeperConstraint(ctx context.Context, clusterID uuid.UUID, name string, generation int64) error {
	q, err := gatekeeperConstraintQueries(ctx)
	if err != nil {
		return err
	}
	deps := runtimeDependencies(ctx)
	if deps.K8s == nil {
		return errors.New("Gatekeeper constraint tunnel requester is not configured")
	}
	row, err := q.GetAuthoredConstraintByName(ctx, sqlc.GetAuthoredConstraintByNameParams{ClusterID: clusterID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load Gatekeeper constraint intent: %w", err)
	}
	if row.Generation != generation {
		return nil
	}
	manifest, err := gatekeeperpolicy.ParseManifest([]byte(row.Yaml))
	if err != nil {
		_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "stored manifest failed validation")
		return fmt.Errorf("stored Gatekeeper manifest is invalid: %w", asynq.SkipRetry)
	}

	var statusCode int
	switch row.DesiredState {
	case "present":
		path := kubeutil.ServerSideApplyPath(manifest.APIPath(), kubeutil.ApplyOptions{
			FieldManager: gatekeeperConstraintFieldManager, Force: true,
		})
		resp, requestErr := deps.K8s.Do(ctx, clusterID.String(), http.MethodPatch, path, manifest.JSON, kubeutil.ApplyPatchHeaders())
		if requestErr != nil {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "tunnel apply failed")
			return fmt.Errorf("apply Gatekeeper constraint: %w", requestErr)
		}
		if resp == nil {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "cluster returned no apply response")
			return errors.New("apply Gatekeeper constraint: empty cluster response")
		}
		statusCode = resp.StatusCode
		if statusCode >= http.StatusBadRequest {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", fmt.Sprintf("cluster rejected apply (status %d)", statusCode))
			return fmt.Errorf("cluster rejected Gatekeeper constraint apply with status %d", statusCode)
		}
	case "absent":
		resp, requestErr := deps.K8s.Do(ctx, clusterID.String(), http.MethodDelete, manifest.APIPath(), nil, map[string]string{"Accept": "application/json"})
		if requestErr != nil {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "tunnel delete failed")
			return fmt.Errorf("delete Gatekeeper constraint: %w", requestErr)
		}
		if resp == nil {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "cluster returned no delete response")
			return errors.New("delete Gatekeeper constraint: empty cluster response")
		}
		statusCode = resp.StatusCode
		if statusCode >= http.StatusBadRequest && statusCode != http.StatusNotFound {
			_ = markGatekeeperConstraintResult(ctx, q, row, "failed", fmt.Sprintf("cluster rejected delete (status %d)", statusCode))
			return fmt.Errorf("cluster rejected Gatekeeper constraint delete with status %d", statusCode)
		}
	default:
		_ = markGatekeeperConstraintResult(ctx, q, row, "failed", "invalid desired state")
		return fmt.Errorf("invalid Gatekeeper constraint desired state %q: %w", row.DesiredState, asynq.SkipRetry)
	}

	if err := markGatekeeperConstraintResult(ctx, q, row, "synced", ""); err != nil {
		return err
	}
	events.PublishChanged(deps.Bus, "gatekeeper_constraint", clusterID.String(), row.ID.String(), map[string]any{
		"name": row.Name, "desired_state": row.DesiredState, "sync_status": "synced",
	})
	return nil
}

func gatekeeperConstraintQueries(ctx context.Context) (gatekeeperConstraintReconcileQuerier, error) {
	q, ok := runtimeDependencies(ctx).Queries.(gatekeeperConstraintReconcileQuerier)
	if !ok || q == nil {
		return nil, errors.New("Gatekeeper constraint database runtime is not configured")
	}
	return q, nil
}

func markGatekeeperConstraintResult(ctx context.Context, q gatekeeperConstraintReconcileQuerier, row sqlc.AuthoredConstraint, status, lastError string) error {
	if len(lastError) > 200 {
		lastError = lastError[:200]
	}
	_, err := q.MarkAuthoredConstraintReconcileResult(ctx, sqlc.MarkAuthoredConstraintReconcileResultParams{
		ClusterID: row.ClusterID, Name: row.Name, ObservedGeneration: row.Generation,
		SyncStatus: status, LastError: lastError,
	})
	if err != nil {
		return fmt.Errorf("record Gatekeeper constraint outcome: %w", err)
	}
	return nil
}
