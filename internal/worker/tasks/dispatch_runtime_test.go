package tasks

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/email"
)

func completeDispatchRuntime(t *testing.T) DispatchRuntime {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encryptor, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return DispatchRuntime{
		Email: EmailDeps{
			Queries:  &fakeEmailQuerier{},
			Sender:   &fakeSender{},
			Provider: fakeProvider{cfg: email.Settings{Enabled: true}},
		},
		Webhook: WebhookDeps{
			Queries:   &fakeWebhookQuerier{},
			Sender:    &fakeWebhookSender{},
			Encryptor: encryptor,
		},
		SIEM: SIEMDeps{
			Queries:   &fakeSIEMQuerier{},
			Encryptor: encryptor,
		},
		TaskOutbox: TaskOutboxDispatchDeps{
			Queries:  &fakeTaskOutboxQuerier{},
			Enqueuer: &fakeTaskOutboxEnqueuer{},
		},
		AuditOutbox: AuditOutboxDispatchDeps{Queries: &fakeAuditOutboxQuerier{}},
		AdminQueue: AdminQueueOperationDeps{
			Queries: &fakeAdminQueueOperationQuerier{}, Inspector: &fakeAdminQueueOperationInspector{},
		},
	}
}

func TestDispatchRuntimeRejectsEveryMissingFamily(t *testing.T) {
	err := (DispatchRuntime{}).Validate()
	if err == nil {
		t.Fatal("empty dispatch runtime validated successfully")
	}
	for _, dependency := range []string{
		"email.queries", "email.sender", "email.settings_provider",
		"webhook.queries", "webhook.sender", "webhook.encryptor",
		"siem.queries", "siem.encryptor",
		"task_outbox.queries", "task_outbox.enqueuer", "audit_outbox.queries",
		"admin_queue.queries", "admin_queue.inspector",
	} {
		if !strings.Contains(err.Error(), dependency) {
			t.Errorf("validation error %q does not report %s", err, dependency)
		}
	}
}

func TestDispatchRuntimeRejectsTypedNilDependency(t *testing.T) {
	runtime := completeDispatchRuntime(t)
	var queries *fakeEmailQuerier
	runtime.Email.Queries = queries
	if err := runtime.Validate(); err == nil || !strings.Contains(err.Error(), "email.queries") {
		t.Fatalf("typed-nil email querier validation error = %v", err)
	}
}

func TestDispatchRuntimeBindsCompleteHandlerFamily(t *testing.T) {
	bindings, err := completeDispatchRuntime(t).HandlerBindings()
	if err != nil {
		t.Fatal(err)
	}
	for _, taskType := range []string{
		EmailDispatchType, EmailCleanupOldType,
		WebhookDispatchType, WebhookCleanupOldType,
		SIEMDispatchType, SIEMCleanupOldType,
		TaskOutboxDispatchType, AuditOutboxDispatchType,
		AdminQueueOperationType,
	} {
		if bindings[taskType] == nil {
			t.Errorf("handler %q is not bound", taskType)
		}
	}
	if len(bindings) != 9 {
		t.Fatalf("handler bindings = %d, want 9", len(bindings))
	}
}
