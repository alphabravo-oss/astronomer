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

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func cloneRegistrationRecords(source map[uuid.UUID]*sqlc.ClusterRegistrationRecord) map[uuid.UUID]*sqlc.ClusterRegistrationRecord {
	cloned := make(map[uuid.UUID]*sqlc.ClusterRegistrationRecord, len(source))
	for id, record := range source {
		copy := *record
		cloned[id] = &copy
	}
	return cloned
}

func fakeClusterRegistrationRunTx(q *fakeRegistrationQuerier) clusterRegistrationRunTxFunc {
	return func(_ context.Context, fn func(ClusterRegistrationMutationTx) error) error {
		q.mu.Lock()
		registrations := cloneRegistrationRecords(q.regs)
		steps := append([]sqlc.ClusterRegistrationStep(nil), q.steps...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		q.mu.Unlock()
		if err := fn(q); err != nil {
			q.mu.Lock()
			q.regs, q.steps, q.audits = registrations, steps, audits
			q.mu.Unlock()
			return err
		}
		return nil
	}
}

func transactionalRegistrationFixture(t *testing.T, phase registration.Phase, auditErr error) (*ClusterRegistrationHandler, *fakeRegistrationQuerier, *events.Bus, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeRegQuerier()
	clusterID, callerID := uuid.New(), uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "production"}
	q.regs[clusterID] = &sqlc.ClusterRegistrationRecord{ID: clusterID, RegistrationPhase: string(phase)}
	q.users[callerID] = sqlc.User{ID: callerID, IsSuperuser: true}
	q.outboxErr = auditErr
	bus := events.NewBus()
	h := NewClusterRegistrationHandler(q, bus)
	h.SetRunTx(fakeClusterRegistrationRunTx(q))
	return h, q, bus, clusterID, callerID
}

func authenticatedRegistrationRequest(method, target string, callerID uuid.UUID, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	return request.WithContext(middleware.SetAuthenticatedUserForTest(request.Context(), &middleware.AuthenticatedUser{
		ID: callerID.String(), AuthMethod: "jwt",
	}))
}

func TestRegistrationAuditFailureRollsBackStateStepsAndEvents(t *testing.T) {
	for _, operation := range []string{"options", "confirm", "retry", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			phase := registration.PhaseCreated
			if operation == "retry" {
				phase = registration.PhaseFailed
			}
			h, q, bus, clusterID, callerID := transactionalRegistrationFixture(t, phase, errors.New("audit-SENTINEL"))
			failedStepID := uuid.New()
			if operation == "retry" {
				q.steps = append(q.steps, sqlc.ClusterRegistrationStep{
					ID: failedStepID, ClusterID: clusterID, StepName: "delivery_applying", Status: "failed", StepOrder: 1,
				})
			}
			beforePhase := q.regs[clusterID].RegistrationPhase
			beforeBaseline := q.regs[clusterID].InstallBaseline
			beforeSteps := len(q.steps)
			ch := pubSubscribe(t, bus)
			router := routerForRegistration(h)
			var request *http.Request
			switch operation {
			case "options":
				request = authenticatedRegistrationRequest(http.MethodPut, "/api/v1/clusters/"+clusterID.String()+"/registration/options/", callerID, `{"install_baseline":true}`)
			case "confirm":
				request = authenticatedRegistrationRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/registration/confirm/", callerID, "")
			case "retry":
				request = authenticatedRegistrationRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/registration/retry/"+failedStepID.String()+"/", callerID, "")
			case "cancel":
				request = authenticatedRegistrationRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/registration/cancel/", callerID, "")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if q.regs[clusterID].RegistrationPhase != beforePhase || q.regs[clusterID].InstallBaseline != beforeBaseline || len(q.steps) != beforeSteps || len(q.audits) != 0 {
				t.Fatalf("rollback retained phase=%s baseline=%+v steps=%d audits=%d",
					q.regs[clusterID].RegistrationPhase, q.regs[clusterID].InstallBaseline, len(q.steps), len(q.audits))
			}
			if operation == "retry" && q.stepLocks != 1 {
				t.Fatalf("retry row-lock reads=%d, want 1", q.stepLocks)
			}
			select {
			case event := <-ch:
				t.Fatalf("rollback published registration event: %+v", event)
			default:
			}
		})
	}
}

func TestRegistrationEffectsFlushAfterTransactionCommit(t *testing.T) {
	h, q, bus, clusterID, callerID := transactionalRegistrationFixture(t, registration.PhaseCreated, nil)
	ch := pubSubscribe(t, bus)
	router := routerForRegistration(h)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, authenticatedRegistrationRequest(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/registration/confirm/", callerID, ""))
	if w.Code != http.StatusOK || len(q.audits) != 1 || q.regs[clusterID].RegistrationPhase != string(registration.PhaseAwaitingAgent) {
		t.Fatalf("status=%d phase=%s audits=%d body=%s", w.Code, q.regs[clusterID].RegistrationPhase, len(q.audits), w.Body.String())
	}
	event := pubReceive(t, ch, events.TypeClusterRegistrationPhase)
	if event.ID == 0 {
		t.Fatal("committed transition did not publish phase event")
	}
}

func TestEveryRegistrationMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("cluster_registration.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"PutOptions": false, "PostConfirm": false, "PostRetry": false, "PostCancel": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeClusterRegistrationMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeClusterRegistrationMutation", name)
		}
	}
}
