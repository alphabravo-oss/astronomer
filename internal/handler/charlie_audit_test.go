package handler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type charlieAuditWriterFake struct {
	row sqlc.CreateAuditLogV1Params
	err error
}

func (f *charlieAuditWriterFake) CreateAuditLogV1(_ context.Context, row sqlc.CreateAuditLogV1Params) error {
	f.row = row
	return f.err
}

func TestCharlieAdminAuditUsesOnlyAllowlistedContentFreeFields(t *testing.T) {
	writer := &charlieAuditWriterFake{}
	request := authenticatedCharlieRequest(http.MethodPatch, "/admin/charlie/mode/resource-SENTINEL?prompt=query-SENTINEL", "", uuid.New(), "jwt")
	request.Header.Set("User-Agent", "agent-SENTINEL")
	recordCharlieAdminAudit(request, writer, "admin.charlie.mode.update", "charlie_connection", "current", map[string]any{
		"mode": "read_only", "revision": int64(7),
	})
	row := writer.row
	if row.Action != "admin.charlie.mode.update" || row.ResourceType != "charlie_connection" || row.ResourceID != "current" {
		t.Fatalf("unexpected Charlie administrator audit identity: %+v", row)
	}
	if row.Path != "" || row.HTTPMethod != "" || row.ResourceName != "" || row.UserAgent != "" || row.IpAddress != nil {
		t.Fatalf("Charlie administrator audit retained HTTP/resource metadata: %+v", row)
	}
	serialized := string(row.Detail) + row.Path + row.ResourceName + row.UserAgent
	for _, sentinel := range []string{"resource-SENTINEL", "query-SENTINEL", "agent-SENTINEL"} {
		if strings.Contains(serialized, sentinel) {
			t.Fatalf("Charlie administrator audit leaked %q: %s", sentinel, serialized)
		}
	}
	for _, expected := range []string{`"outcome_code":"completed"`, `"mode":"read_only"`, `"revision":7`} {
		if !strings.Contains(string(row.Detail), expected) {
			t.Fatalf("Charlie administrator audit lacks %s: %s", expected, row.Detail)
		}
	}
}

func TestRequiredCharlieAdminAuditPersistsIntentWithoutMutatingFields(t *testing.T) {
	writer := &charlieAuditWriterFake{}
	request := authenticatedCharlieRequest(http.MethodPatch, "/", "", uuid.New(), "jwt")
	fields := map[string]any{"mode": "auto", "revision": int64(8)}
	if err := requireCharlieAdminAudit(request, writer, "admin.charlie.mode.update", "charlie_connection", "current", fields); err != nil {
		t.Fatalf("required admin audit failed: %v", err)
	}
	if !strings.Contains(string(writer.row.Detail), `"outcome_code":"authorized"`) {
		t.Fatalf("required admin audit lacks intent outcome: %s", writer.row.Detail)
	}
	if _, mutated := fields["outcome_code"]; mutated {
		t.Fatal("admin audit mutated caller-owned detail fields")
	}
}

func TestCharlieAdminAuditRejectsArbitraryResourceAndLogsOnlyFailureCode(t *testing.T) {
	var output bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(prior) })
	writer := &charlieAuditWriterFake{}
	request := authenticatedCharlieRequest(http.MethodPut, "/", "", uuid.New(), "jwt")
	recordCharlieAdminAudit(request, writer, "admin.charlie.action_policy.update", "charlie_action_policy", "resource-SENTINEL", nil)
	if writer.row.Action != "" {
		t.Fatal("invalid resource reached Charlie audit storage")
	}
	failing := &charlieAuditWriterFake{err: errors.New("database-SENTINEL")}
	recordCharlieAdminAudit(request, failing, "admin.charlie.action_policy.update", "charlie_action_policy", strings.Repeat("a", 64), nil)
	if failing.row.Action != "admin.charlie.action_policy.update" {
		t.Fatal("valid content-free audit did not reach the failing storage probe")
	}
	if !strings.Contains(output.String(), "charlie.admin_audit_persist_failed") || strings.Contains(output.String(), "resource-SENTINEL") || strings.Contains(output.String(), "database-SENTINEL") {
		t.Fatalf("unsafe Charlie audit failure log: %s", output.String())
	}
}
