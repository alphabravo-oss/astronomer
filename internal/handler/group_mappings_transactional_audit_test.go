package handler

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func fakeGroupMappingsRunTx(f *fakeGroupMappings) groupMappingsRunTxFunc {
	return func(_ context.Context, fn func(GroupMappingsMutationTx) error) error {
		mappings := cloneUUIDMap(f.mappings)
		global := cloneUUIDMap(f.global)
		cluster := cloneUUIDMap(f.cluster)
		project := cloneUUIDMap(f.project)
		audits := f.auditCalls
		if err := fn(f); err != nil {
			f.mappings, f.global, f.cluster, f.project, f.auditCalls = mappings, global, cluster, project, audits
			return err
		}
		return nil
	}
}

func cloneUUIDMap[T any](source map[uuid.UUID]T) map[uuid.UUID]T {
	cloned := make(map[uuid.UUID]T, len(source))
	for id, value := range source {
		cloned[id] = value
	}
	return cloned
}

type countingGroupRBACInvalidator struct{ calls int }

func (c *countingGroupRBACInvalidator) Invalidate(string) { c.calls++ }

func TestGroupMappingAuditFailureRollsBackCRUDAndResync(t *testing.T) {
	caller, target, connectorID, roleID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, operation := range []string{"create", "delete", "resync"} {
		t.Run(operation, func(t *testing.T) {
			f := newFakeMappings()
			f.user = sqlc.User{ID: caller, IsSuperuser: true}
			mappingID := uuid.New()
			if operation != "create" {
				f.mappings[mappingID] = sqlc.IdentityGroupMapping{ID: mappingID, GroupName: "engineering", Scope: "global", RoleID: roleID, ConnectorID: pgtype.UUID{Bytes: connectorID, Valid: true}}
			}
			if operation == "resync" {
				f.user.ID = target
				f.snapshot = sqlc.UserIdpGroup{UserID: target, ConnectorID: pgtype.UUID{Bytes: connectorID, Valid: true}, Groups: []byte(`["engineering"]`)}
			}
			f.outboxErr = errors.New("audit-SENTINEL")
			h := NewGroupMappingsHandler(f)
			h.SetRunTx(fakeGroupMappingsRunTx(f))
			invalidator := &countingGroupRBACInvalidator{}
			h.SetRBACCacheInvalidator(invalidator)
			w := httptest.NewRecorder()
			switch operation {
			case "create":
				body := []byte(`{"group_name":"engineering","scope":"global","role_id":"` + roleID.String() + `"}`)
				h.Create(w, makeAuthedRequest(t, http.MethodPost, "/api/v1/admin/group-mappings/", body, caller, ""))
			case "delete":
				h.Delete(w, makeAuthedRequest(t, http.MethodDelete, "/api/v1/admin/group-mappings/"+mappingID.String()+"/", nil, caller, mappingID.String()))
			case "resync":
				h.ResyncUser(w, makeAuthedRequest(t, http.MethodPost, "/api/v1/admin/users/"+target.String()+"/resync-groups/", nil, caller, target.String()))
			}

			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if operation == "create" && len(f.mappings) != 0 {
				t.Fatalf("create rollback retained mappings=%+v", f.mappings)
			}
			if operation == "delete" {
				if _, ok := f.mappings[mappingID]; !ok {
					t.Fatal("delete rollback removed mapping")
				}
			}
			if operation == "resync" && len(f.global) != 0 {
				t.Fatalf("resync rollback retained bindings=%+v", f.global)
			}
			if invalidator.calls != 0 {
				t.Fatalf("rollback invalidated RBAC cache %d times", invalidator.calls)
			}
			if f.auditCalls != 0 {
				t.Fatalf("rollback retained audit calls=%d", f.auditCalls)
			}
		})
	}
}

func TestEveryGroupMappingMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("group_mappings.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Delete": false, "ResyncUser": false}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeGroupMappingsMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeGroupMappingsMutation", name)
		}
	}
}
