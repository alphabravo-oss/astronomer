package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

type queueInspectorFake struct {
	queues  map[string]*asynq.QueueInfo
	tasks   map[string]map[string]*asynq.TaskInfo
	runTask string
}

func (f *queueInspectorFake) Queues() ([]string, error) { return []string{"default"}, nil }
func (f *queueInspectorFake) GetQueueInfo(name string) (*asynq.QueueInfo, error) {
	if info := f.queues[name]; info != nil {
		return info, nil
	}
	return nil, errors.New("missing")
}
func (f *queueInspectorFake) listTasks(name string, state asynq.TaskState) ([]*asynq.TaskInfo, error) {
	items := []*asynq.TaskInfo{}
	for _, item := range f.tasks[name] {
		if item.State == state {
			items = append(items, item)
		}
	}
	return items, nil
}
func (f *queueInspectorFake) ListPendingTasks(name string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.listTasks(name, asynq.TaskStatePending)
}
func (f *queueInspectorFake) ListActiveTasks(name string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.listTasks(name, asynq.TaskStateActive)
}
func (f *queueInspectorFake) ListScheduledTasks(name string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.listTasks(name, asynq.TaskStateScheduled)
}
func (f *queueInspectorFake) ListRetryTasks(name string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.listTasks(name, asynq.TaskStateRetry)
}
func (f *queueInspectorFake) ListArchivedTasks(name string, _ ...asynq.ListOption) ([]*asynq.TaskInfo, error) {
	return f.listTasks(name, asynq.TaskStateArchived)
}
func (f *queueInspectorFake) GetTaskInfo(queue, id string) (*asynq.TaskInfo, error) {
	if task := f.tasks[queue][id]; task != nil {
		return task, nil
	}
	return nil, errors.New("missing")
}
func (f *queueInspectorFake) RunTask(queue, id string) error {
	task := f.tasks[queue][id]
	if task == nil {
		return errors.New("missing")
	}
	f.runTask = queue + "/" + id
	task.State = asynq.TaskStatePending
	return nil
}

func TestQueueCapabilityRedactsPayloadsAndErrorStrings(t *testing.T) {
	inspector := &queueInspectorFake{
		queues: map[string]*asynq.QueueInfo{"default": {Queue: "default", Size: 1, Archived: 1}},
		tasks:  map[string]map[string]*asynq.TaskInfo{"default": {"task-a": {ID: "task-a", Type: "catalog:sync", State: asynq.TaskStateArchived, Payload: []byte(`{"api_key":"SENTINEL"}`), LastErr: "password=SENTINEL", LastFailedAt: time.Now()}}},
	}
	adapter, _ := NewQueueCapabilityAdapter(inspector)
	descriptor, _ := capabilityByName("astronomer.queue.failed_tasks")
	result, err := adapter.Execute(context.Background(), descriptor, map[string]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "SENTINEL") || strings.Contains(string(result), "password") {
		t.Fatalf("queue metadata leaked payload/error: %s", result)
	}
}

func TestQueueCapabilityListsPendingTasksWithPurposeAndNoPayloadValues(t *testing.T) {
	inspector := &queueInspectorFake{
		queues: map[string]*asynq.QueueInfo{},
		tasks: map[string]map[string]*asynq.TaskInfo{"default": {
			"task-a": {
				ID: "task-a", Type: "catalog:sync", State: asynq.TaskStatePending,
				Payload:  []byte(`{"repository_url":"https://user:SENTINEL@example.invalid/private","api_key":"SENTINEL"}`),
				MaxRetry: 25,
			},
		}},
	}
	adapter, _ := NewQueueCapabilityAdapter(inspector)
	descriptor, _ := capabilityByName("astronomer.queue.tasks")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{
		"state": "pending", "queue": "default", "page": 1, "page_size": 10,
	}))
	if err != nil {
		t.Fatal(err)
	}
	value := string(result)
	if strings.Contains(value, "SENTINEL") || strings.Contains(value, "repository_url") || !strings.Contains(value, "Synchronizes configured Helm repositories") {
		t.Fatalf("unsafe or incomplete pending task list: %s", value)
	}
}

func TestQueueCapabilityGetsSafeTaskDiagnostics(t *testing.T) {
	failedAt := time.Now().UTC()
	inspector := &queueInspectorFake{
		queues: map[string]*asynq.QueueInfo{},
		tasks: map[string]map[string]*asynq.TaskInfo{"default": {
			"task-a": {
				ID: "task-a", Type: "catalog:sync", State: asynq.TaskStateArchived,
				Payload: []byte(`{"repository_url":"https://user:SENTINEL@example.invalid/private","api_key":"SENTINEL"}`),
				LastErr: "request failed: 401 authorization token=SENTINEL", LastFailedAt: failedAt,
				Retried: 2, MaxRetry: 5, Timeout: 30 * time.Second,
			},
		}},
	}
	adapter, _ := NewQueueCapabilityAdapter(inspector)
	descriptor, _ := capabilityByName("astronomer.queue.task_get")
	result, err := adapter.Execute(context.Background(), descriptor, rawArguments(t, map[string]any{"task_id": "task-a"}))
	if err != nil {
		t.Fatal(err)
	}
	value := string(result)
	for _, wanted := range []string{`"failure_code":"authentication"`, `"payload_fields":["api_key","repository_url"]`, `"retry_remaining":3`, `"timeout_seconds":30`} {
		if !strings.Contains(value, wanted) {
			t.Fatalf("task detail missing %s: %s", wanted, value)
		}
	}
	if strings.Contains(value, "SENTINEL") || strings.Contains(value, "example.invalid") {
		t.Fatalf("task diagnostics leaked payload/error values: %s", value)
	}
}

func TestQueueCapabilityRetriesOnlyAllowlistedTaskAndVerifies(t *testing.T) {
	inspector := &queueInspectorFake{queues: map[string]*asynq.QueueInfo{}, tasks: map[string]map[string]*asynq.TaskInfo{
		"default": {"task-a": {ID: "task-a", Type: "catalog:sync", State: asynq.TaskStateArchived}},
	}}
	adapter, _ := NewQueueCapabilityAdapter(inspector)
	descriptor, _ := capabilityByName("astronomer.queue.retry_task")
	args := rawArguments(t, map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	result, err := adapter.Execute(context.Background(), descriptor, args)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := adapter.Verify(context.Background(), descriptor, args, result)
	if err != nil || !verified || inspector.runTask != "default/task-a" {
		t.Fatalf("retry verification=%v err=%v run=%s", verified, err, inspector.runTask)
	}
}

func TestQueueCapabilityRejectsDownstreamTaskType(t *testing.T) {
	inspector := &queueInspectorFake{queues: map[string]*asynq.QueueInfo{}, tasks: map[string]map[string]*asynq.TaskInfo{
		"default": {"task-a": {ID: "task-a", Type: "cluster_template:apply", State: asynq.TaskStateArchived}},
	}}
	adapter, _ := NewQueueCapabilityAdapter(inspector)
	descriptor, _ := capabilityByName("astronomer.queue.retry_task")
	args := rawArguments(t, map[string]any{"resource_id": "resource-a", "task_id": "task-a", "operation_id": "action-a"})
	if _, err := adapter.Execute(context.Background(), descriptor, args); err == nil || inspector.runTask != "" {
		t.Fatal("downstream task was retried")
	}
}
