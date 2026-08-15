package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hibiken/asynq"
)

type queueCapabilityInspector interface {
	Queues() ([]string, error)
	Servers() ([]*asynq.ServerInfo, error)
	GetQueueInfo(string) (*asynq.QueueInfo, error)
	ListPendingTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListActiveTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListScheduledTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListRetryTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	ListArchivedTasks(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	GetTaskInfo(string, string) (*asynq.TaskInfo, error)
	RunTask(string, string) error
}

type QueueCapabilityAdapter struct{ inspector queueCapabilityInspector }

var charlieQueueNames = []string{"critical", "default", "low"}

var retriableManagementTaskTypes = map[string]struct{}{
	"catalog:sync": {}, "metrics:aggregate": {}, "auth:refresh_group_sync_metrics": {},
	"anomaly:baseline_recompute": {}, "anomaly:xcluster_recompute": {},
	"chart_recommendations:recompute": {}, "crd_mirror:gauge_populate": {},
}

// Task purposes are static product knowledge, not inferred from a payload. They
// let Charlie explain the scheduler without disclosing task arguments or
// teaching the model to guess from an implementation-looking type name.
var managementTaskPurposes = map[string]string{
	"catalog:sync":                    "Synchronizes configured Helm repositories into Astronomer's installable chart catalog.",
	"metrics:aggregate":               "Aggregates management-plane metrics used by Astronomer dashboards and health views.",
	"auth:refresh_group_sync_metrics": "Refreshes management-plane identity group synchronization metrics.",
	"anomaly:baseline_recompute":      "Recomputes management-plane anomaly detection baselines.",
	"anomaly:xcluster_recompute":      "Recomputes fleet-level anomaly aggregates from state already held by Astronomer.",
	"chart_recommendations:recompute": "Recomputes Astronomer's chart recommendation aggregates.",
	"crd_mirror:gauge_populate":       "Refreshes management-plane CRD mirror health gauges.",
	"charlie:trigger_dispatch":        "Dispatches one Astronomer management-plane trigger event to Charlie; it is separate from catalog synchronization.",
}

func NewQueueCapabilityAdapter(inspector queueCapabilityInspector) (*QueueCapabilityAdapter, error) {
	if inspector == nil {
		return nil, fmt.Errorf("Charlie queue capability inspector is unavailable")
	}
	return &QueueCapabilityAdapter{inspector: inspector}, nil
}

func QueueCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	return map[string]CapabilityExecutor{
		"astronomer.queue.health": adapter, "astronomer.queue.failed_tasks": adapter,
		"astronomer.queue.tasks": adapter, "astronomer.queue.task_get": adapter,
		"astronomer.queue.retry_task": adapter,
	}
}

func (a *QueueCapabilityAdapter) Execute(_ context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	switch capability.Name {
	case "astronomer.queue.health":
		consumers, consumersAvailable := a.queueConsumers()
		materialized, materializationAvailable := a.materializedQueues()
		items := []map[string]any{}
		for _, queue := range charlieQueueNames {
			consumer := consumers[queue]
			if materializationAvailable && !materialized[queue] {
				items = append(items, emptyQueueSummary(queue, consumer, consumersAvailable))
				continue
			}
			info, err := a.inspector.GetQueueInfo(queue)
			if errors.Is(err, asynq.ErrQueueNotFound) {
				items = append(items, emptyQueueSummary(queue, consumer, consumersAvailable))
				continue
			}
			if err != nil {
				items = append(items, unavailableQueueSummary(queue, consumer, consumersAvailable))
				continue
			}
			items = append(items, queueSummary(info, consumer, consumersAvailable))
		}
		return marshalBounded(map[string]any{
			"queues": items, "consumer_inspection_available": consumersAvailable,
			"materialization_inspection_available": materializationAvailable,
		}, capability.MaxResponseBytes)
	case "astronomer.queue.failed_tasks":
		page, size := pagination(arguments, 50)
		wantedType := stringArgument(arguments, "task_type")
		items := []map[string]any{}
		unavailable := []string{}
		for _, queue := range charlieQueueNames {
			tasks, err := a.inspector.ListArchivedTasks(queue, asynq.Page(int(page)), asynq.PageSize(int(size)))
			if errors.Is(err, asynq.ErrQueueNotFound) {
				continue
			}
			if err != nil {
				unavailable = append(unavailable, queue)
				continue
			}
			for _, task := range tasks {
				if task == nil || (wantedType != "" && task.Type != wantedType) {
					continue
				}
				items = append(items, safeTaskSummary(queue, task))
				if len(items) >= int(size) {
					break
				}
			}
			if len(items) >= int(size) {
				break
			}
		}
		sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["task_id"]) < fmt.Sprint(items[j]["task_id"]) })
		return marshalBounded(map[string]any{
			"items": items, "page": page, "page_size": size,
			"partial": len(unavailable) > 0, "unavailable_queues": unavailable,
		}, capability.MaxResponseBytes)
	case "astronomer.queue.tasks":
		page, size := pagination(arguments, 50)
		state := stringArgument(arguments, "state")
		if state == "" {
			state = "pending"
		}
		wantedQueue := stringArgument(arguments, "queue")
		wantedType := stringArgument(arguments, "task_type")
		items := []map[string]any{}
		unavailable := []string{}
		for _, queue := range charlieQueueNames {
			if wantedQueue != "" && queue != wantedQueue {
				continue
			}
			tasks, err := a.listTasks(queue, state, asynq.Page(int(page)), asynq.PageSize(int(size)))
			if errors.Is(err, asynq.ErrQueueNotFound) {
				continue
			}
			if err != nil {
				unavailable = append(unavailable, queue)
				continue
			}
			for _, task := range tasks {
				if task == nil || (wantedType != "" && task.Type != wantedType) {
					continue
				}
				items = append(items, safeTaskSummary(queue, task))
				if len(items) >= int(size) {
					break
				}
			}
			if len(items) >= int(size) {
				break
			}
		}
		sort.Slice(items, func(i, j int) bool {
			left, right := fmt.Sprint(items[i]["queue"]), fmt.Sprint(items[j]["queue"])
			if left == right {
				return fmt.Sprint(items[i]["task_id"]) < fmt.Sprint(items[j]["task_id"])
			}
			return left < right
		})
		return marshalBounded(map[string]any{
			"items": items, "state": state, "page": page, "page_size": size,
			"partial": len(unavailable) > 0, "unavailable_queues": unavailable,
		}, capability.MaxResponseBytes)
	case "astronomer.queue.task_get":
		queue, task, err := a.findTask(stringArgument(arguments, "task_id"))
		if err != nil {
			return nil, err
		}
		return marshalBounded(safeTaskDetail(queue, task), capability.MaxResponseBytes)
	case "astronomer.queue.retry_task":
		taskID := stringArgument(arguments, "task_id")
		queue, task, err := a.findTask(taskID)
		if err != nil {
			return nil, err
		}
		if task.State != asynq.TaskStateArchived && task.State != asynq.TaskStateRetry {
			return nil, fmt.Errorf("management task is not retryable from its current state")
		}
		if _, allowed := retriableManagementTaskTypes[task.Type]; !allowed {
			return nil, fmt.Errorf("management task type is not allowlisted")
		}
		if err := a.inspector.RunTask(queue, taskID); err != nil {
			return nil, err
		}
		return marshalBounded(operationResult(arguments, "accepted", "queue/"+queue+"/task/"+taskID), capability.MaxResponseBytes)
	default:
		return nil, fmt.Errorf("unsupported queue capability")
	}
}

func (a *QueueCapabilityAdapter) listTasks(queue, state string, options ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	switch state {
	case "pending":
		return a.inspector.ListPendingTasks(queue, options...)
	case "active":
		return a.inspector.ListActiveTasks(queue, options...)
	case "scheduled":
		return a.inspector.ListScheduledTasks(queue, options...)
	case "retry":
		return a.inspector.ListRetryTasks(queue, options...)
	case "archived":
		return a.inspector.ListArchivedTasks(queue, options...)
	default:
		return nil, fmt.Errorf("management task state is unsupported")
	}
}

func (a *QueueCapabilityAdapter) Verify(_ context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, _ json.RawMessage) (bool, error) {
	if capability.Effect == EffectRead {
		return true, nil
	}
	_, task, err := a.findTask(stringArgument(arguments, "task_id"))
	if err != nil {
		return false, err
	}
	return task.State != asynq.TaskStateArchived && task.State != asynq.TaskStateRetry, nil
}

func (a *QueueCapabilityAdapter) findTask(taskID string) (string, *asynq.TaskInfo, error) {
	for _, queue := range charlieQueueNames {
		task, err := a.inspector.GetTaskInfo(queue, taskID)
		if err == nil && task != nil {
			return queue, task, nil
		}
	}
	return "", nil, fmt.Errorf("management task was not found in an allowlisted queue")
}

type queueConsumerSummary struct {
	Servers     int
	Concurrency int
	Weight      int
}

func (a *QueueCapabilityAdapter) materializedQueues() (map[string]bool, bool) {
	queues, err := a.inspector.Queues()
	if err != nil {
		return map[string]bool{}, false
	}
	result := make(map[string]bool, len(queues))
	for _, queue := range queues {
		result[queue] = true
	}
	return result, true
}

func (a *QueueCapabilityAdapter) queueConsumers() (map[string]queueConsumerSummary, bool) {
	servers, err := a.inspector.Servers()
	if err != nil {
		return map[string]queueConsumerSummary{}, false
	}
	result := make(map[string]queueConsumerSummary, len(charlieQueueNames))
	for _, server := range servers {
		if server == nil || !strings.EqualFold(strings.TrimSpace(server.Status), "active") {
			continue
		}
		for queue, weight := range server.Queues {
			if weight <= 0 {
				continue
			}
			value := result[queue]
			value.Servers++
			value.Concurrency += server.Concurrency
			value.Weight += weight
			result[queue] = value
		}
	}
	return result, true
}

func queueSummary(info *asynq.QueueInfo, consumer queueConsumerSummary, consumersAvailable bool) map[string]any {
	value := map[string]any{
		"queue": info.Queue, "available": true, "materialized": true,
		"size": info.Size, "active": info.Active,
		"pending": info.Pending, "scheduled": info.Scheduled, "retry": info.Retry,
		"archived": info.Archived, "completed": info.Completed, "paused": info.Paused,
		"latency_seconds": info.Latency.Seconds(),
	}
	addQueueConsumerSummary(value, consumer, consumersAvailable)
	return value
}

func emptyQueueSummary(queue string, consumer queueConsumerSummary, consumersAvailable bool) map[string]any {
	value := map[string]any{
		"queue": queue, "available": true, "materialized": false,
		"size": 0, "active": 0, "pending": 0, "scheduled": 0, "retry": 0,
		"archived": 0, "completed": 0, "paused": false, "latency_seconds": 0,
	}
	addQueueConsumerSummary(value, consumer, consumersAvailable)
	return value
}

func unavailableQueueSummary(queue string, consumer queueConsumerSummary, consumersAvailable bool) map[string]any {
	value := map[string]any{"queue": queue, "available": false, "materialized": false, "inspection_code": "queue_inspection_unavailable"}
	addQueueConsumerSummary(value, consumer, consumersAvailable)
	return value
}

func addQueueConsumerSummary(value map[string]any, consumer queueConsumerSummary, available bool) {
	value["consumer_inspection_available"] = available
	if !available {
		return
	}
	value["consumer_ready"] = consumer.Servers > 0
	value["consumer_servers"] = consumer.Servers
	value["consumer_concurrency"] = consumer.Concurrency
	value["consumer_weight"] = consumer.Weight
}

func safeTaskSummary(queue string, task *asynq.TaskInfo) map[string]any {
	_, retryAllowed := retriableManagementTaskTypes[task.Type]
	value := map[string]any{
		"queue": queue, "task_id": task.ID, "task_type": task.Type,
		"state": task.State.String(), "retried": task.Retried,
		"max_retry": task.MaxRetry, "retry_allowed": retryAllowed,
		"orphaned": task.IsOrphaned,
	}
	if purpose := managementTaskPurposes[task.Type]; purpose != "" {
		value["purpose"] = purpose
	}
	if !task.NextProcessAt.IsZero() {
		value["next_process_at"] = task.NextProcessAt.UTC()
	}
	if !task.LastFailedAt.IsZero() {
		value["last_failed_at"] = task.LastFailedAt.UTC()
		value["failure_code"] = classifyTaskFailure(task.LastErr)
	}
	return value
}

func safeTaskDetail(queue string, task *asynq.TaskInfo) map[string]any {
	value := safeTaskSummary(queue, task)
	value["payload_bytes"] = len(task.Payload)
	value["payload_fields"] = taskPayloadFields(task.Payload)
	value["timeout_seconds"] = int64(task.Timeout.Seconds())
	value["retry_remaining"] = max(task.MaxRetry-task.Retried, 0)
	if !task.Deadline.IsZero() {
		value["deadline"] = task.Deadline.UTC()
	}
	if !task.CompletedAt.IsZero() {
		value["completed_at"] = task.CompletedAt.UTC()
	}
	if task.Group != "" {
		value["group"] = task.Group
	}
	return value
}

func taskPayloadFields(payload []byte) []string {
	if len(payload) == 0 {
		return []string{}
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(payload, &value) != nil {
		return []string{}
	}
	fields := make([]string, 0, len(value))
	for field := range value {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) > 32 {
		fields = fields[:32]
	}
	return fields
}

// classifyTaskFailure intentionally returns a fixed diagnostic category rather
// than Asynq's raw LastErr. Worker errors may contain repository URLs,
// credentials, query text, or user-controlled values.
func classifyTaskFailure(message string) string {
	value := strings.ToLower(message)
	switch {
	case value == "":
		return "unreported"
	case strings.Contains(value, "context canceled") || strings.Contains(value, "cancelled"):
		return "cancelled"
	case strings.Contains(value, "deadline exceeded") || strings.Contains(value, "timeout") || strings.Contains(value, "timed out"):
		return "timeout"
	case strings.Contains(value, "rate limit") || strings.Contains(value, "too many requests") || strings.Contains(value, " 429"):
		return "rate_limited"
	case strings.Contains(value, "unauthorized") || strings.Contains(value, "forbidden") || strings.Contains(value, "credential") || strings.Contains(value, " 401") || strings.Contains(value, " 403"):
		return "authentication"
	case strings.Contains(value, "certificate") || strings.Contains(value, "tls") || strings.Contains(value, "x509"):
		return "tls"
	case strings.Contains(value, "no such host") || strings.Contains(value, "dns"):
		return "dns"
	case strings.Contains(value, "connection refused") || strings.Contains(value, "connection reset") || strings.Contains(value, "network"):
		return "network"
	case strings.Contains(value, "not found") || strings.Contains(value, " 404"):
		return "not_found"
	case strings.Contains(value, "unmarshal") || strings.Contains(value, "invalid") || strings.Contains(value, "unsupported"):
		return "invalid_input"
	case strings.Contains(value, "database") || strings.Contains(value, "postgres") || strings.Contains(value, "sqlstate"):
		return "database"
	default:
		return "internal"
	}
}
