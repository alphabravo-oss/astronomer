package handler

import (
	"context"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type monitoringMutationTestTx struct {
	MonitoringQuerier
}

func (tx monitoringMutationTestTx) UpsertAuditOutbox(ctx context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if writer, ok := tx.MonitoringQuerier.(interface {
		CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error
	}); ok {
		if err := writer.CreateAuditLogV1(ctx, auditLogParamsFromOutbox(arg)); err != nil {
			return sqlc.AuditOutbox{}, err
		}
	}
	return sqlc.AuditOutbox{}, nil
}

func (tx monitoringMutationTestTx) CreateMonitoringOperationIdempotent(ctx context.Context, arg sqlc.CreateMonitoringOperationIdempotentParams) (sqlc.MonitoringOperation, error) {
	op, err := tx.CreateMonitoringOperation(ctx, sqlc.CreateMonitoringOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
	op.TargetType = arg.TargetType
	op.TargetKey = arg.TargetKey
	op.OperationType = arg.OperationType
	op.Payload = arg.Payload
	return op, err
}

func (tx monitoringMutationTestTx) DeleteDefaultMonitoringBackendIfUnused(ctx context.Context, id uuid.UUID) (sqlc.MonitoringBackend, error) {
	deleter, ok := tx.MonitoringQuerier.(monitoringBackendDeleter)
	if !ok {
		return sqlc.MonitoringBackend{}, errMonitoringBackendDeleteUnsupported
	}
	return deleter.DeleteDefaultMonitoringBackendIfUnused(ctx, id)
}

func wireMonitoringMutationTestTx(h *MonitoringHandler, q MonitoringQuerier) *MonitoringHandler {
	tx := monitoringMutationTestTx{MonitoringQuerier: q}
	h.SetRunTx(func(_ context.Context, fn func(MonitoringMutationTx) error) error { return fn(tx) })
	return h
}

func newMonitoringHandlerWithQueriesForTest(q MonitoringQuerier, requester K8sRequester) *MonitoringHandler {
	return wireMonitoringMutationTestTx(NewMonitoringHandlerWithQueries(q, requester), q)
}

func newMonitoringHandlerWithDepsForTest(q MonitoringQuerier, requester K8sRequester, helm HelmRequester) *MonitoringHandler {
	return wireMonitoringMutationTestTx(NewMonitoringHandlerWithDeps(q, requester, helm), q)
}
