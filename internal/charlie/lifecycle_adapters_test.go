package charlie

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type failingLifecycleAuditWriter struct{}

func (failingLifecycleAuditWriter) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return errors.New("database-SENTINEL")
}

func TestDBLifecycleAuditorLogsBoundedFailureWithoutPayload(t *testing.T) {
	var output bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })

	auditor := NewDBLifecycleAuditor(failingLifecycleAuditWriter{})
	auditor.RecordCharlieSessionLifecycle(context.Background(), SessionLifecycleAudit{
		Action: "charlie.session.SENTINEL", SessionID: uuid.New(), ActorID: uuid.New(),
		Visibility: "private-SENTINEL", OutcomeCode: "outcome-SENTINEL", ResourceCount: 1,
	})

	logged := output.String()
	if !strings.Contains(logged, "charlie.lifecycle_audit_persist_failed") {
		t.Fatalf("audit failure code was not logged: %s", logged)
	}
	for _, secret := range []string{"database-SENTINEL", "private-SENTINEL", "outcome-SENTINEL", "charlie.session.SENTINEL"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("lifecycle audit failure log leaked payload %q: %s", secret, logged)
		}
	}
}
