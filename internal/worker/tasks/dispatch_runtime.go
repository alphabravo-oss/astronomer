package tasks

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// DispatchRuntime is the immutable, worker-owned composition root for durable
// outbound delivery and outbox handlers. Handler closures are bound from this
// value at worker construction time; no package-global ConfigureX state is
// consulted while tasks execute.
type DispatchRuntime struct {
	Email       EmailDeps
	Webhook     WebhookDeps
	SIEM        SIEMDeps
	TaskOutbox  TaskOutboxDispatchDeps
	AuditOutbox AuditOutboxDispatchDeps
	AdminQueue  AdminQueueOperationDeps

	siemState *siemDispatchState
}

type siemDispatchState struct {
	forwarderLockMu sync.Mutex
	forwarderLocks  map[uuid.UUID]*sync.Mutex
	httpClientMu    sync.Mutex
	httpClients     map[uuid.UUID]cachedSIEMHTTPClient
}

func (runtime DispatchRuntime) normalized() DispatchRuntime {
	if runtime.SIEM.HTTPClient == nil {
		runtime.SIEM.HTTPClient = httpclient.New(10 * time.Second)
	}
	if runtime.siemState == nil {
		runtime.siemState = &siemDispatchState{
			forwarderLocks: make(map[uuid.UUID]*sync.Mutex),
			httpClients:    make(map[uuid.UUID]cachedSIEMHTTPClient),
		}
	}
	if runtime.SIEM.TransportFactory == nil {
		runtime.SIEM.TransportFactory = runtime.defaultSIEMTransportFactory
	}
	return runtime
}

// Validate rejects incomplete handler graphs before the worker begins queue
// consumption. Optional clock functions and SIEM production defaults are
// normalized rather than treated as missing dependencies.
func (runtime DispatchRuntime) Validate() error {
	runtime = runtime.normalized()
	return validateRequiredRuntimeDependencies("dispatcher", []requiredRuntimeDependency{
		{name: "email.queries", value: runtime.Email.Queries},
		{name: "email.sender", value: runtime.Email.Sender},
		{name: "email.settings_provider", value: runtime.Email.Provider},
		{name: "webhook.queries", value: runtime.Webhook.Queries},
		{name: "webhook.sender", value: runtime.Webhook.Sender},
		{name: "webhook.encryptor", value: runtime.Webhook.Encryptor},
		{name: "siem.queries", value: runtime.SIEM.Queries},
		{name: "siem.encryptor", value: runtime.SIEM.Encryptor},
		{name: "siem.http_client", value: runtime.SIEM.HTTPClient},
		{name: "siem.transport_factory", value: runtime.SIEM.TransportFactory},
		{name: "task_outbox.queries", value: runtime.TaskOutbox.Queries},
		{name: "task_outbox.enqueuer", value: runtime.TaskOutbox.Enqueuer},
		{name: "audit_outbox.queries", value: runtime.AuditOutbox.Queries},
		{name: "admin_queue.queries", value: runtime.AdminQueue.Queries},
		{name: "admin_queue.inspector", value: runtime.AdminQueue.Inspector},
	})
}

// HandlerBindings returns the complete dispatcher task family keyed by the
// canonical Asynq task type.
func (runtime DispatchRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	runtime = runtime.normalized()
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate dispatch runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		EmailDispatchType:       runtime.HandleEmailDispatch,
		EmailCleanupOldType:     runtime.HandleEmailCleanupOld,
		WebhookDispatchType:     runtime.HandleWebhookDispatch,
		WebhookCleanupOldType:   runtime.HandleWebhookCleanupOld,
		SIEMDispatchType:        runtime.HandleSIEMDispatch,
		SIEMCleanupOldType:      runtime.HandleSIEMCleanupOld,
		TaskOutboxDispatchType:  runtime.HandleTaskOutboxDispatch,
		AuditOutboxDispatchType: runtime.HandleAuditOutboxDispatch,
		AdminQueueOperationType: runtime.HandleAdminQueueOperation,
	}, nil
}
