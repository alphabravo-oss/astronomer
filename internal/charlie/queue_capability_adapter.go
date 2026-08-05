package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hibiken/asynq"
)

type queueCapabilityInspector interface {
	Queues() ([]string, error)
	GetQueueInfo(string) (*asynq.QueueInfo, error)
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

func NewQueueCapabilityAdapter(inspector queueCapabilityInspector) (*QueueCapabilityAdapter, error) {
	if inspector == nil {
		return nil, fmt.Errorf("Charlie queue capability inspector is unavailable")
	}
	return &QueueCapabilityAdapter{inspector: inspector}, nil
}

func QueueCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	return map[string]CapabilityExecutor{
		"astronomer.queue.health": adapter, "astronomer.queue.failed_tasks": adapter,
		"astronomer.queue.retry_task": adapter,
	}
}

func (a *QueueCapabilityAdapter) Execute(_ context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	switch capability.Name {
	case "astronomer.queue.health":
		items := []map[string]any{}
		for _, queue := range charlieQueueNames {
			info, err := a.inspector.GetQueueInfo(queue)
			if err != nil {
				items = append(items, map[string]any{"queue": queue, "available": false})
				continue
			}
			items = append(items, queueSummary(info))
		}
		return marshalBounded(map[string]any{"queues": items}, capability.MaxResponseBytes)
	case "astronomer.queue.failed_tasks":
		page, size := pagination(arguments, 50)
		wantedType := stringArgument(arguments, "task_type")
		items := []map[string]any{}
		for _, queue := range charlieQueueNames {
			tasks, err := a.inspector.ListArchivedTasks(queue, asynq.Page(int(page)), asynq.PageSize(int(size)))
			if err != nil {
				continue
			}
			for _, task := range tasks {
				if task == nil || (wantedType != "" && task.Type != wantedType) {
					continue
				}
				_, retryAllowed := retriableManagementTaskTypes[task.Type]
				items = append(items, map[string]any{"queue": queue, "task_id": task.ID, "task_type": task.Type, "retried": task.Retried, "last_failed_at": task.LastFailedAt.UTC(), "retry_allowed": retryAllowed})
				if len(items) >= int(size) {
					break
				}
			}
			if len(items) >= int(size) {
				break
			}
		}
		sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["task_id"]) < fmt.Sprint(items[j]["task_id"]) })
		return marshalBounded(map[string]any{"items": items, "page": page, "page_size": size}, capability.MaxResponseBytes)
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

func queueSummary(info *asynq.QueueInfo) map[string]any {
	return map[string]any{
		"queue": info.Queue, "available": true, "size": info.Size, "active": info.Active,
		"pending": info.Pending, "scheduled": info.Scheduled, "retry": info.Retry,
		"archived": info.Archived, "completed": info.Completed, "paused": info.Paused,
		"latency_seconds": info.Latency.Seconds(),
	}
}
