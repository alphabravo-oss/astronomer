package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

type deliveryAuditWriter struct {
	rows []sqlc.UpsertAuditOutboxParams
}

func (w *deliveryAuditWriter) UpsertAuditOutbox(_ context.Context, row sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	w.rows = append(w.rows, row)
	return sqlc.AuditOutbox{ID: row.ID}, nil
}

func TestRecordAuditOutboxPreservesRequestIdentityAndMetadataOnlyDetail(t *testing.T) {
	writer := &deliveryAuditWriter{}
	userID := uuid.New()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/sources", nil)
	request.Header.Set("User-Agent", "delivery-audit-test")
	request = request.WithContext(reqctx.WithUser(request.Context(), &reqctx.User{
		ID: userID.String(), AuthMethod: "jwt",
	}))

	err := recordAuditOutbox(request, writer, deliveryAuditEvent{action: "delivery.source.created", resourceType: "delivery_source", resourceID: "source-a", resourceName: "source", status: http.StatusCreated, detail: map[string]any{
		"project_id":            uuid.NewString(),
		"source_type":           "git",
		"credential_configured": true,
	}})
	if err != nil {
		t.Fatal(err)
	}

	if len(writer.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(writer.rows))
	}
	row := writer.rows[0]
	if row.Action != "delivery.source.created" || row.ResourceType != "delivery_source" || row.ResourceID != "source-a" ||
		row.HttpMethod != http.MethodPost || row.Path != "/api/v1/delivery/sources" || row.ActorAuthMethod != "jwt" ||
		!row.UserID.Valid || uuid.UUID(row.UserID.Bytes) != userID {
		t.Fatalf("unexpected delivery audit row: %+v", row)
	}
	var detail map[string]any
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail["source_type"] != "git" || detail["credential_configured"] != true {
		t.Fatalf("unexpected delivery audit detail: %s", row.Detail)
	}
}

func TestDeliveryDynamicAuditActionsMatchContract(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z]+(\.[a-z0-9_]+)+$`)
	for _, action := range []string{
		rolloutAuditAction(rollout.ActionPause),
		rolloutAuditAction(rollout.ActionResume),
		rolloutAuditAction(rollout.ActionAbort),
		rolloutAuditAction(rollout.ActionRetry),
		rolloutAuditAction(rollout.ActionRollback),
		deploymentAuditAction(deployment.ActionReconcile),
		deploymentAuditAction(deployment.ActionSuspend),
	} {
		if !pattern.MatchString(action) {
			t.Fatalf("delivery audit action %q does not match contract", action)
		}
	}
}
