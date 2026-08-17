package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// workPipelineQueries is deliberately composed from fixed sqlc operations.
// It is not a raw SQL or generic database capability: Charlie can select only
// the bounded product-owned projections registered in the catalog.
type workPipelineQueries interface {
	ListTaskOutbox(context.Context, sqlc.ListTaskOutboxParams) ([]sqlc.TaskOutbox, error)
	ListTaskOutboxFiltered(context.Context, sqlc.ListTaskOutboxFilteredParams) ([]sqlc.TaskOutbox, error)
	CountTaskOutbox(context.Context, string) (int64, error)
	GetTaskOutbox(context.Context, uuid.UUID) (sqlc.TaskOutbox, error)
	RetryTaskOutbox(context.Context, sqlc.RetryTaskOutboxParams) (sqlc.TaskOutbox, error)
	ListDeferredOperations(context.Context, sqlc.ListDeferredOperationsParams) ([]sqlc.DeferredOperation, error)

	ListCatalogOperations(context.Context, sqlc.ListCatalogOperationsParams) ([]sqlc.CatalogOperation, error)
	GetCatalogOperation(context.Context, uuid.UUID) (sqlc.CatalogOperation, error)
	ListCatalogOperationEvents(context.Context, uuid.UUID) ([]sqlc.CatalogOperationEvent, error)
	ListToolOperations(context.Context, sqlc.ListToolOperationsParams) ([]sqlc.ToolOperation, error)
	GetToolOperation(context.Context, uuid.UUID) (sqlc.ToolOperation, error)
	ListToolOperationEvents(context.Context, uuid.UUID) ([]sqlc.ToolOperationEvent, error)
	ListMonitoringOperations(context.Context, sqlc.ListMonitoringOperationsParams) ([]sqlc.MonitoringOperation, error)
	GetMonitoringOperation(context.Context, uuid.UUID) (sqlc.MonitoringOperation, error)
	ListMonitoringOperationEvents(context.Context, uuid.UUID) ([]sqlc.MonitoringOperationEvent, error)
	ListLoggingOperations(context.Context, sqlc.ListLoggingOperationsParams) ([]sqlc.LoggingOperation, error)
	GetLoggingOperation(context.Context, uuid.UUID) (sqlc.LoggingOperation, error)
	ListLoggingOperationEvents(context.Context, uuid.UUID) ([]sqlc.LoggingOperationEvent, error)
	ListWorkloadOperations(context.Context, sqlc.ListWorkloadOperationsParams) ([]sqlc.WorkloadOperation, error)
	GetWorkloadOperation(context.Context, uuid.UUID) (sqlc.WorkloadOperation, error)
	ListWorkloadOperationEvents(context.Context, uuid.UUID) ([]sqlc.WorkloadOperationEvent, error)

	ListControlPlaneAlerts(context.Context, sqlc.ListControlPlaneAlertsParams) ([]sqlc.ControlPlaneAlert, error)
}

type WorkPipelineCapabilityAdapter struct {
	queries workPipelineQueries
	now     func() time.Time
}

func NewWorkPipelineCapabilityAdapter(queries workPipelineQueries) (*WorkPipelineCapabilityAdapter, error) {
	if queries == nil {
		return nil, fmt.Errorf("Charlie work-pipeline capability database is unavailable")
	}
	return &WorkPipelineCapabilityAdapter{queries: queries, now: time.Now}, nil
}

func WorkPipelineCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	registrations := map[string]CapabilityExecutor{}
	for _, name := range []string{
		"astronomer.task_outbox.summary", "astronomer.task_outbox.list", "astronomer.task_outbox.get", "astronomer.task_outbox.retry_delivery",
		"astronomer.scheduler.health", "astronomer.controllers.summary", "astronomer.controllers.alerts",
		"astronomer.catalog.operations", "astronomer.catalog.operation_get",
		"astronomer.tools.operations", "astronomer.tools.operation_get",
		"astronomer.monitoring.operations", "astronomer.monitoring.operation_get",
		"astronomer.logging.operations", "astronomer.logging.operation_get",
		"astronomer.workloads.operations", "astronomer.workloads.operation_get",
	} {
		registrations[name] = adapter
	}
	return registrations
}

func (a *WorkPipelineCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	var value any
	var err error
	switch capability.Name {
	case "astronomer.task_outbox.summary":
		value, err = a.outboxSummary(ctx)
	case "astronomer.task_outbox.list":
		value, err = a.outboxList(ctx, arguments)
	case "astronomer.task_outbox.get":
		value, err = a.outboxGet(ctx, arguments)
	case "astronomer.task_outbox.retry_delivery":
		value, err = a.outboxRetry(ctx, arguments)
	case "astronomer.scheduler.health":
		value, err = a.schedulerHealth(ctx)
	case "astronomer.controllers.summary":
		value, err = a.controllerSummary(ctx)
	case "astronomer.controllers.alerts":
		value, err = a.controllerAlerts(ctx, arguments)
	default:
		domain, detail := pipelineOperationCapability(capability.Name)
		if domain == "" {
			return nil, fmt.Errorf("unsupported work-pipeline capability")
		}
		if detail {
			value, err = a.operationGet(ctx, domain, arguments)
		} else {
			value, err = a.operationList(ctx, domain, arguments)
		}
	}
	if err != nil {
		return nil, err
	}
	return marshalBounded(value, capability.MaxResponseBytes)
}

func (a *WorkPipelineCapabilityAdapter) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, _ json.RawMessage) (bool, error) {
	if capability.Name != "astronomer.task_outbox.retry_delivery" {
		return true, nil
	}
	id, err := uuid.Parse(stringArgument(arguments, "outbox_id"))
	if err != nil {
		return false, err
	}
	row, err := a.queries.GetTaskOutbox(ctx, id)
	if err != nil {
		return false, err
	}
	return row.Status == "pending" && row.LastError == "" && !row.LockedUntil.Valid, nil
}

func pipelineOperationCapability(name string) (string, bool) {
	for _, domain := range []string{"catalog", "tools", "monitoring", "logging", "workloads"} {
		if name == "astronomer."+domain+".operations" {
			return domain, false
		}
		if name == "astronomer."+domain+".operation_get" {
			return domain, true
		}
	}
	return "", false
}

func (a *WorkPipelineCapabilityAdapter) outboxSummary(ctx context.Context) (map[string]any, error) {
	counts := map[string]int64{}
	for _, status := range []string{"pending", "delivering", "failed", "delivered", "dead"} {
		count, err := a.queries.CountTaskOutbox(ctx, status)
		if err != nil {
			return nil, err
		}
		counts[status] = count
	}
	oldest, err := a.queries.ListTaskOutbox(ctx, sqlc.ListTaskOutboxParams{Status: "pending", Limit: 100})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"states": counts, "oldest_pending_age_seconds": oldestOutboxAge(oldest, a.now().UTC()),
		"queue_task_id_contract": "task_outbox.id is the downstream Asynq task_id after delivery",
	}, nil
}

func (a *WorkPipelineCapabilityAdapter) outboxList(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	status, taskType := stringArgument(arguments, "status"), stringArgument(arguments, "task_type")
	rows, err := a.queries.ListTaskOutboxFiltered(ctx, sqlc.ListTaskOutboxFilteredParams{Status: status, TaskType: taskType, Limit: int32(size), Offset: int32((page - 1) * size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeOutboxRecord(row))
	}
	return map[string]any{"items": items, "page": page, "page_size": size, "status": status}, nil
}

func (a *WorkPipelineCapabilityAdapter) outboxGet(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	id, err := uuid.Parse(stringArgument(arguments, "outbox_id"))
	if err != nil {
		return nil, fmt.Errorf("task outbox id is invalid")
	}
	row, err := a.queries.GetTaskOutbox(ctx, id)
	if err != nil {
		return nil, err
	}
	return safeOutboxRecord(row), nil
}

func (a *WorkPipelineCapabilityAdapter) outboxRetry(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	id, err := uuid.Parse(stringArgument(arguments, "outbox_id"))
	if err != nil {
		return nil, fmt.Errorf("task outbox id is invalid")
	}
	existing, err := a.queries.GetTaskOutbox(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Status != "failed" && existing.Status != "dead" {
		return nil, fmt.Errorf("task delivery is not retryable from its current state")
	}
	row, err := a.queries.RetryTaskOutbox(ctx, sqlc.RetryTaskOutboxParams{
		ID: id, NextAttemptAt: pgtype.Timestamptz{Time: a.now().UTC(), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	result := operationResult(arguments, "accepted", "task_outbox/"+row.ID.String())
	result["previous_status"] = existing.Status
	result["task_type"] = row.TaskType
	return result, nil
}

func safeOutboxRecord(row sqlc.TaskOutbox) map[string]any {
	value := map[string]any{
		"outbox_id": row.ID.String(), "queue_task_id": row.ID.String(), "task_type": row.TaskType,
		"queue": row.QueueName, "status": row.Status, "attempt_count": row.AttemptCount,
		"max_delivery_attempts": row.MaxDeliveryAttempts, "max_retry": row.MaxRetry,
		"timeout_seconds": row.TimeoutSeconds, "payload_bytes": len(row.Payload),
		"payload_fields": taskPayloadFields(row.Payload), "created_at": row.CreatedAt.UTC(), "updated_at": row.UpdatedAt.UTC(),
	}
	if purpose := managementTaskPurposes[row.TaskType]; purpose != "" {
		value["purpose"] = purpose
	}
	if row.DedupeKey.Valid {
		value["dedupe_key_present"] = true
		value["dedupe_key_digest"] = digestBytes([]byte(row.DedupeKey.String))
	}
	if !row.NextAttemptAt.IsZero() {
		value["next_attempt_at"] = row.NextAttemptAt.UTC()
	}
	if row.LockedUntil.Valid {
		value["locked_until"] = row.LockedUntil.Time.UTC()
	}
	if row.DeliveredAt.Valid {
		value["delivered_at"] = row.DeliveredAt.Time.UTC()
	}
	if row.LastError != "" {
		value["failure_code"] = classifyTaskFailure(row.LastError)
	}
	return value
}

func oldestOutboxAge(rows []sqlc.TaskOutbox, now time.Time) int64 {
	var oldest time.Time
	for _, row := range rows {
		if oldest.IsZero() || row.CreatedAt.Before(oldest) {
			oldest = row.CreatedAt
		}
	}
	if oldest.IsZero() || now.Before(oldest) {
		return 0
	}
	return int64(now.Sub(oldest).Seconds())
}

func (a *WorkPipelineCapabilityAdapter) schedulerHealth(ctx context.Context) (map[string]any, error) {
	outbox, err := a.outboxSummary(ctx)
	if err != nil {
		return nil, err
	}
	deferred, err := a.queries.ListDeferredOperations(ctx, sqlc.ListDeferredOperationsParams{Limit: 100})
	if err != nil {
		return nil, err
	}
	deferredStates := map[string]int{}
	oldestPending := time.Time{}
	for _, row := range deferred {
		deferredStates[row.Status]++
		if row.Status == "pending" && (oldestPending.IsZero() || row.CreatedAt.Before(oldestPending)) {
			oldestPending = row.CreatedAt
		}
	}
	deferredAge := int64(0)
	if !oldestPending.IsZero() && a.now().After(oldestPending) {
		deferredAge = int64(a.now().Sub(oldestPending).Seconds())
	}
	return map[string]any{
		"task_outbox": outbox, "deferred_operations": map[string]any{
			"sampled": len(deferred), "states": deferredStates, "oldest_pending_age_seconds": deferredAge,
		},
	}, nil
}

func (a *WorkPipelineCapabilityAdapter) controllerSummary(ctx context.Context) (map[string]any, error) {
	controllers := map[string]any{}
	for _, domain := range []string{"catalog", "tools", "monitoring", "logging", "workloads"} {
		items, err := a.listOperations(ctx, domain, "", 1, 100)
		if err != nil {
			return nil, err
		}
		states := map[string]int{}
		for _, item := range items {
			states[fmt.Sprint(item["status"])]++
		}
		controllers[domain] = map[string]any{"sampled": len(items), "states": states}
	}
	alerts, err := a.queries.ListControlPlaneAlerts(ctx, sqlc.ListControlPlaneAlertsParams{Limit: 100, Status: pgtype.Text{String: "active", Valid: true}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"controllers": controllers, "active_alerts": len(alerts)}, nil
}

func (a *WorkPipelineCapabilityAdapter) controllerAlerts(ctx context.Context, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	params := sqlc.ListControlPlaneAlertsParams{Limit: int32(size), Offset: int32((page - 1) * size)}
	if status := stringArgument(arguments, "status"); status != "" {
		params.Status = pgtype.Text{String: status, Valid: true}
	}
	if controller := stringArgument(arguments, "controller"); controller != "" {
		params.Controller = pgtype.Text{String: controller, Valid: true}
	}
	rows, err := a.queries.ListControlPlaneAlerts(ctx, params)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"alert_id": row.ID.String(), "controller": row.Controller, "condition_type": row.ConditionType,
			"status": row.Status, "fired_at": row.FiredAt.UTC(), "updated_at": row.UpdatedAt.UTC(),
			"diagnostic_code": classifyTaskFailure(row.Message), "detail_fields": taskPayloadFields(row.Detail),
		}
		if row.ResolvedAt.Valid {
			item["resolved_at"] = row.ResolvedAt.Time.UTC()
		}
		if row.AcknowledgedAt.Valid {
			item["acknowledged_at"] = row.AcknowledgedAt.Time.UTC()
		}
		items = append(items, item)
	}
	return map[string]any{"items": items, "page": page, "page_size": size}, nil
}

func (a *WorkPipelineCapabilityAdapter) operationList(ctx context.Context, domain string, arguments map[string]json.RawMessage) (map[string]any, error) {
	page, size := pagination(arguments, 50)
	items, err := a.listOperations(ctx, domain, stringArgument(arguments, "status"), page, size)
	if err != nil {
		return nil, err
	}
	return map[string]any{"domain": domain, "items": items, "page": page, "page_size": size}, nil
}

func (a *WorkPipelineCapabilityAdapter) listOperations(ctx context.Context, domain, status string, page, size int64) ([]map[string]any, error) {
	filter := pgtype.Text{}
	if status != "" {
		filter = pgtype.Text{String: status, Valid: true}
	}
	limit, offset := int32(size), int32((page-1)*size)
	items := []map[string]any{}
	switch domain {
	case "catalog":
		rows, err := a.queries.ListCatalogOperations(ctx, sqlc.ListCatalogOperationsParams{Limit: limit, Offset: offset, Status: filter})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			items = append(items, safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
		}
	case "tools":
		rows, err := a.queries.ListToolOperations(ctx, sqlc.ListToolOperationsParams{Limit: limit, Offset: offset, Status: filter})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			items = append(items, safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
		}
	case "monitoring":
		rows, err := a.queries.ListMonitoringOperations(ctx, sqlc.ListMonitoringOperationsParams{Limit: limit, Offset: offset, Status: filter})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			items = append(items, safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
		}
	case "logging":
		rows, err := a.queries.ListLoggingOperations(ctx, sqlc.ListLoggingOperationsParams{Limit: limit, Offset: offset, Status: filter})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			items = append(items, safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
		}
	case "workloads":
		rows, err := a.queries.ListWorkloadOperations(ctx, sqlc.ListWorkloadOperationsParams{Limit: limit, Offset: offset, Status: filter})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			items = append(items, safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt))
		}
	default:
		return nil, fmt.Errorf("operation domain is unsupported")
	}
	return items, nil
}

func (a *WorkPipelineCapabilityAdapter) operationGet(ctx context.Context, domain string, arguments map[string]json.RawMessage) (map[string]any, error) {
	id, err := uuid.Parse(stringArgument(arguments, "record_id"))
	if err != nil {
		return nil, fmt.Errorf("operation id is invalid")
	}
	var operation map[string]any
	events := []safePipelineEvent{}
	switch domain {
	case "catalog":
		row, err := a.queries.GetCatalogOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		operation = safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
		rows, err := a.queries.ListCatalogOperationEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			events = append(events, safeEvent(row.Level, row.Stage, row.Message, row.Detail, row.CreatedAt))
		}
	case "tools":
		row, err := a.queries.GetToolOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		operation = safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
		rows, err := a.queries.ListToolOperationEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			events = append(events, safeEvent(row.Level, row.Stage, row.Message, row.Detail, row.CreatedAt))
		}
	case "monitoring":
		row, err := a.queries.GetMonitoringOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		operation = safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
		rows, err := a.queries.ListMonitoringOperationEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			events = append(events, safeEvent(row.Level, row.Stage, row.Message, row.Detail, row.CreatedAt))
		}
	case "logging":
		row, err := a.queries.GetLoggingOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		operation = safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
		rows, err := a.queries.ListLoggingOperationEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			events = append(events, safeEvent(row.Level, row.Stage, row.Message, row.Detail, row.CreatedAt))
		}
	case "workloads":
		row, err := a.queries.GetWorkloadOperation(ctx, id)
		if err != nil {
			return nil, err
		}
		operation = safeOperation(row.ID, domain, row.TargetType, row.TargetKey, row.OperationType, row.Status, row.AttemptCount, row.Payload, row.ErrorMessage, row.StartedAt, row.CompletedAt, row.CreatedAt, row.UpdatedAt)
		rows, err := a.queries.ListWorkloadOperationEvents(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			events = append(events, safeEvent(row.Level, row.Stage, row.Message, row.Detail, row.CreatedAt))
		}
	default:
		return nil, fmt.Errorf("operation domain is unsupported")
	}
	operation["events"] = events
	return operation, nil
}

func safeOperation(id uuid.UUID, domain, targetType, targetKey, operationType, status string, attempts int32, payload json.RawMessage, failure string, started, completed pgtype.Timestamptz, created, updated time.Time) map[string]any {
	value := map[string]any{
		"operation_id": id.String(), "domain": domain, "target_type": boundedDiagnosticText(targetType, 64),
		"target_ref": boundedDiagnosticText(targetKey, 128), "operation_type": boundedDiagnosticText(operationType, 64),
		"status": status, "attempt_count": attempts, "payload_bytes": len(payload), "payload_fields": taskPayloadFields(payload),
		"created_at": created.UTC(), "updated_at": updated.UTC(),
	}
	if failure != "" {
		value["failure_code"] = classifyTaskFailure(failure)
	}
	if started.Valid {
		value["started_at"] = started.Time.UTC()
	}
	if completed.Valid {
		value["completed_at"] = completed.Time.UTC()
	}
	return value
}

type safePipelineEvent struct {
	Level          string    `json:"level"`
	Stage          string    `json:"stage"`
	DiagnosticCode string    `json:"diagnostic_code"`
	DetailFields   []string  `json:"detail_fields"`
	CreatedAt      time.Time `json:"created_at"`
}

func safeEvent(level, stage, message string, detail json.RawMessage, created time.Time) safePipelineEvent {
	return safePipelineEvent{
		Level: boundedDiagnosticText(level, 16), Stage: boundedDiagnosticText(stage, 64),
		DiagnosticCode: classifyTaskFailure(message), DetailFields: taskPayloadFields(detail), CreatedAt: created.UTC(),
	}
}

func boundedDiagnosticText(value string, maximum int) string {
	lines := redactLogLines([]byte(value), 1)
	if len(lines) == 0 {
		return ""
	}
	value = lines[0]
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}
