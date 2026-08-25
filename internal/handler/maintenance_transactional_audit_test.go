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
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func fakeMaintenanceRunTx(q *fakeMaintenanceQuerier) maintenanceRunTxFunc {
	return func(_ context.Context, fn func(MaintenanceMutationTx) error) error {
		createCalls, updateCalls, deleteCalls := q.createCalled, q.updateCalled, q.deleteCalled
		cancelCalls, auditCalls := q.cancelCalled, q.auditCalls
		if err := fn(q); err != nil {
			q.createCalled, q.updateCalled, q.deleteCalled = createCalls, updateCalls, deleteCalls
			q.cancelCalled, q.auditCalls = cancelCalls, auditCalls
			return err
		}
		return nil
	}
}

func maintenanceMutationRequest(method, target, id string, caller uuid.UUID, body any) *http.Request {
	r := makeMaintenanceRequest(method, target, caller, body)
	if id == "" {
		return r
	}
	return withURLParam(r, "id", id)
}

func validMaintenanceRequest(name string) MaintenanceWindowRequest {
	return MaintenanceWindowRequest{
		Name: name, Mode: maintenance.ModeBlackout, CronOpen: "0 9 * * 1-5",
		DurationMinutes: 60, Timezone: "UTC", OnBlock: maintenance.OnBlockRefuse,
	}
}

func TestMaintenanceAuditFailureRollsBackEveryMutation(t *testing.T) {
	callerID, windowID, deferredID := uuid.New(), uuid.New(), uuid.New()
	for _, operation := range []string{"create", "update", "delete", "cancel_deferred"} {
		t.Run(operation, func(t *testing.T) {
			window := sqlc.MaintenanceWindow{
				ID: windowID, Name: "business-hours", Mode: maintenance.ModeBlackout,
				CronOpen: "0 9 * * 1-5", DurationMinutes: 60, Timezone: "UTC",
				OnBlock: maintenance.OnBlockRefuse, Enabled: true,
				ClusterSelector: []byte("{}"), OperationTypes: []byte("[]"),
			}
			q := &fakeMaintenanceQuerier{
				user: sqlc.User{ID: callerID, IsSuperuser: true}, windows: []sqlc.MaintenanceWindow{window},
				createdRow: window, updatedRow: window,
				getDeferredRow: sqlc.DeferredOperation{ID: deferredID, WindowID: windowID, OperationType: maintenance.OpClusterDelete, Status: "pending"},
				outboxErr:      errors.New("audit-SENTINEL"),
			}
			h := NewMaintenanceHandler(q, nil)
			h.SetRunTx(fakeMaintenanceRunTx(q))
			w := httptest.NewRecorder()
			switch operation {
			case "create":
				h.Create(w, maintenanceMutationRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", "", callerID, validMaintenanceRequest("new-window")))
			case "update":
				h.Update(w, maintenanceMutationRequest(http.MethodPut, "/api/v1/admin/maintenance-windows/"+windowID.String()+"/", windowID.String(), callerID, validMaintenanceRequest(window.Name)))
			case "delete":
				h.Delete(w, maintenanceMutationRequest(http.MethodDelete, "/api/v1/admin/maintenance-windows/"+windowID.String()+"/", windowID.String(), callerID, nil))
			case "cancel_deferred":
				h.CancelDeferred(w, maintenanceMutationRequest(http.MethodPost, "/api/v1/admin/deferred-operations/"+deferredID.String()+"/cancel/", deferredID.String(), callerID, nil))
			}

			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if q.createCalled != 0 || q.updateCalled != 0 || q.deleteCalled != 0 || q.cancelCalled != 0 || q.auditCalls != 0 {
				t.Fatalf("rollback retained effects create=%d update=%d delete=%d cancel=%d audit=%d",
					q.createCalled, q.updateCalled, q.deleteCalled, q.cancelCalled, q.auditCalls)
			}
		})
	}
}

type countingMaintenanceWindowQuerier struct{ calls int }

func (q *countingMaintenanceWindowQuerier) ListEnabledMaintenanceWindows(context.Context) ([]sqlc.MaintenanceWindow, error) {
	q.calls++
	return nil, nil
}

func TestMaintenanceCacheInvalidatesOnlyAfterCommit(t *testing.T) {
	callerID := uuid.New()
	cacheQueries := &countingMaintenanceWindowQuerier{}
	evaluator := maintenance.NewEvaluator(cacheQueries)
	if _, err := evaluator.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := &fakeMaintenanceQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		createdRow: sqlc.MaintenanceWindow{
			ID: uuid.New(), Name: "new-window", Mode: maintenance.ModeBlackout, CronOpen: "0 9 * * 1-5",
			DurationMinutes: 60, Timezone: "UTC", OnBlock: maintenance.OnBlockRefuse, Enabled: true,
			ClusterSelector: []byte("{}"), OperationTypes: []byte("[]"),
		},
		outboxErr: errors.New("audit unavailable"),
	}
	h := NewMaintenanceHandler(q, evaluator)
	h.SetRunTx(fakeMaintenanceRunTx(q))

	w := httptest.NewRecorder()
	h.Create(w, maintenanceMutationRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", "", callerID, validMaintenanceRequest("new-window")))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("rollback status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := evaluator.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cacheQueries.calls != 1 {
		t.Fatalf("rollback invalidated evaluator cache: database reads=%d, want 1", cacheQueries.calls)
	}

	q.outboxErr = nil
	w = httptest.NewRecorder()
	h.Create(w, maintenanceMutationRequest(http.MethodPost, "/api/v1/admin/maintenance-windows/", "", callerID, validMaintenanceRequest("new-window")))
	if w.Code != http.StatusCreated {
		t.Fatalf("commit status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := evaluator.Windows(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cacheQueries.calls != 2 {
		t.Fatalf("commit did not invalidate evaluator cache: database reads=%d, want 2", cacheQueries.calls)
	}
}

func TestEveryMaintenanceMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("maintenance.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "CancelDeferred": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeMaintenanceMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeMaintenanceMutation", name)
		}
	}
}

func fakeMaintenanceGateRunTx(q *gateMaintenanceQuerier) maintenanceGateRunTxFunc {
	return func(_ context.Context, fn func(MaintenanceGateMutationTx) error) error {
		createdRow := q.createdRow
		calls, idempotentCalls, outboxCalls := q.calls, q.idemCalls, q.outboxCalls
		idempotencyRows := make(map[string]sqlc.DeferredOperation, len(q.idemByKey))
		for key, row := range q.idemByKey {
			idempotencyRows[key] = row
		}
		if err := fn(q); err != nil {
			q.createdRow = createdRow
			q.calls, q.idemCalls, q.outboxCalls = calls, idempotentCalls, outboxCalls
			q.idemByKey = idempotencyRows
			return err
		}
		return nil
	}
}

func TestDeferredGateCommitsOperationAndAuditTogether(t *testing.T) {
	callerID := uuid.New()
	window := sqlc.MaintenanceWindow{
		ID: uuid.New(), Name: "always-defer", Mode: maintenance.ModeBlackout,
		CronOpen: "0 0 * * *", DurationMinutes: 24 * 60, Timezone: "UTC",
		OnBlock: maintenance.OnBlockDefer, Enabled: true,
		ClusterSelector: []byte("{}"), OperationTypes: []byte("[]"),
	}
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantStatus int
		wantCommit int
	}{
		{name: "commit", wantStatus: http.StatusAccepted, wantCommit: 1},
		{name: "audit failure rolls back", auditErr: errors.New("audit-SENTINEL"), wantStatus: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &gateMaintenanceQuerier{
				createdRow: sqlc.DeferredOperation{ID: uuid.New(), Status: "pending"},
				outboxErr:  tc.auditErr,
			}
			gate := NewMaintenanceGate(maintenance.NewEvaluator(&gateEvalQuerier{rows: []sqlc.MaintenanceWindow{window}}), q, gateTestCipher{})
			gate.SetRunTx(fakeMaintenanceGateRunTx(q))
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/target/", nil)
			req.Header.Set("Idempotency-Key", "defer-transaction-proof")
			req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
				ID: callerID.String(), AuthMethod: "jwt",
			}))
			w := httptest.NewRecorder()
			if !EnforceMaintenanceWindow(w, req, gate, maintenance.OpClusterDelete, nil, pgtype.UUID{}, pgtype.UUID{}) {
				t.Fatal("gate did not handle blocked operation")
			}
			if w.Code != tc.wantStatus || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if q.idemCalls != tc.wantCommit || q.outboxCalls != tc.wantCommit || len(q.idemByKey) != tc.wantCommit {
				t.Fatalf("committed effects idempotent=%d outbox=%d rows=%d, want %d",
					q.idemCalls, q.outboxCalls, len(q.idemByKey), tc.wantCommit)
			}
			if tc.wantCommit == 1 && q.auditCalls != 0 {
				t.Fatalf("successful defer used best-effort audit %d times", q.auditCalls)
			}
		})
	}
}
