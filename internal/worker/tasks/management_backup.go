package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const ManagementBackupReconcileType = "management_backup:reconcile"
const ManagementBackupOperationType = "management_backup:operation"

type ManagementBackupPayload struct {
	DestinationID string `json:"destination_id"`
	Generation    int64  `json:"generation,omitempty"`
	OperationID   string `json:"operation_id,omitempty"`
}

type ManagementBackupExecutor interface {
	ManagementBackupReady() bool
	ReconcileManagementBackup(context.Context, uuid.UUID, int64) error
	ExecuteManagementBackupOperation(context.Context, uuid.UUID) error
}

func managementBackupRuntimeDependency(executor ManagementBackupExecutor) any {
	if executor == nil || !executor.ManagementBackupReady() {
		return nil
	}
	return executor
}

func NewManagementBackupReconcileTask(id uuid.UUID, generation int64) (*asynq.Task, error) {
	if id == uuid.Nil || generation <= 0 {
		return nil, errors.New("management backup reconcile requires destination and generation")
	}
	body, err := json.Marshal(ManagementBackupPayload{DestinationID: id.String(), Generation: generation})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(ManagementBackupReconcileType, body, asynq.MaxRetry(8), asynq.Timeout(5*time.Minute)), nil
}

func NewManagementBackupOperationTask(id uuid.UUID) (*asynq.Task, error) {
	if id == uuid.Nil {
		return nil, errors.New("management backup operation requires operation id")
	}
	body, err := json.Marshal(ManagementBackupPayload{OperationID: id.String()})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(ManagementBackupOperationType, body, asynq.MaxRetry(20), asynq.Timeout(5*time.Minute)), nil
}

func HandleManagementBackupReconcile(ctx context.Context, task *asynq.Task) error {
	var p ManagementBackupPayload
	if task == nil || json.Unmarshal(task.Payload(), &p) != nil {
		return fmt.Errorf("%w: invalid management backup payload", asynq.SkipRetry)
	}
	id, err := uuid.Parse(p.DestinationID)
	if err != nil || p.Generation <= 0 {
		return fmt.Errorf("%w: invalid management backup identity", asynq.SkipRetry)
	}
	executor := runtimeDependencies(ctx).ManagementBackup
	if executor == nil {
		return errors.New("management backup executor is not configured")
	}
	return executor.ReconcileManagementBackup(ctx, id, p.Generation)
}

func HandleManagementBackupOperation(ctx context.Context, task *asynq.Task) error {
	var p ManagementBackupPayload
	if task == nil || json.Unmarshal(task.Payload(), &p) != nil {
		return fmt.Errorf("%w: invalid management backup payload", asynq.SkipRetry)
	}
	id, err := uuid.Parse(p.OperationID)
	if err != nil {
		return fmt.Errorf("%w: invalid management backup operation identity", asynq.SkipRetry)
	}
	executor := runtimeDependencies(ctx).ManagementBackup
	if executor == nil {
		return errors.New("management backup executor is not configured")
	}
	return executor.ExecuteManagementBackupOperation(ctx, id)
}
