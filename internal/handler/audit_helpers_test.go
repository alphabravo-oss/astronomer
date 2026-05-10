package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type fakeHandlerAuditWriterV1Only struct {
	lastV1 *sqlc.CreateAuditLogV1Params
}

func (f *fakeHandlerAuditWriterV1Only) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.lastV1 = &arg
	return nil
}

func TestRecordAuditAs_V1OnlyWriter(t *testing.T) {
	writer := &fakeHandlerAuditWriterV1Only{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/", nil)
	req.Header.Set("User-Agent", "handler-audit-test")
	req.RemoteAddr = "198.51.100.7:1234"
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		AuthMethod: "jwt",
	})
	req = req.WithContext(ctx)

	recordAuditAs(req, writer, pgtype.UUID{}, "auth.login_failed", "auth", "", "", map[string]any{
		"email":    "user@example.com",
		"password": "secret",
	})

	if writer.lastV1 == nil {
		t.Fatal("expected audit v1 row to be written")
	}
	if writer.lastV1.Action != "auth.login_failed" {
		t.Fatalf("action = %q, want auth.login_failed", writer.lastV1.Action)
	}
	if writer.lastV1.ActorAuthMethod != "jwt" {
		t.Fatalf("actor_auth_method = %q, want jwt", writer.lastV1.ActorAuthMethod)
	}
	if writer.lastV1.Source != "service" {
		t.Fatalf("source = %q, want service", writer.lastV1.Source)
	}
	var detail map[string]any
	if err := json.Unmarshal(writer.lastV1.Detail, &detail); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if detail["password"] != "[redacted]" {
		t.Fatalf("password detail = %v, want [redacted]", detail["password"])
	}
}
