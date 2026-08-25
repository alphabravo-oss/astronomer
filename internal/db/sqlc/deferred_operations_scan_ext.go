package sqlc

// deferredOperationSelectColumns and scanDeferredOperationRow are shared by
// the hand-authored idempotent deferred-operation query. Canonical CRUD is
// generated from maintenance.sql; keeping only this narrow helper avoids the
// former platform-specific duplicate CRUD implementation.
const deferredOperationSelectColumns = `
    id, window_id, operation_type, operation_spec, target_cluster_id,
    target_project_id, status, deferred_until, expires_at, requested_by,
    last_error, dispatched_at, created_at, updated_at`

func scanDeferredOperationRow(row interface {
	Scan(dest ...any) error
}) (DeferredOperation, error) {
	var operation DeferredOperation
	err := row.Scan(
		&operation.ID,
		&operation.WindowID,
		&operation.OperationType,
		&operation.OperationSpec,
		&operation.TargetClusterID,
		&operation.TargetProjectID,
		&operation.Status,
		&operation.DeferredUntil,
		&operation.ExpiresAt,
		&operation.RequestedBy,
		&operation.LastError,
		&operation.DispatchedAt,
		&operation.CreatedAt,
		&operation.UpdatedAt,
	)
	return operation, err
}
