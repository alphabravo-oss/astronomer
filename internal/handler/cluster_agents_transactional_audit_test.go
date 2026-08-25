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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

func cloneAgentOperations(source map[uuid.UUID][]sqlc.AgentLifecycleOperation) map[uuid.UUID][]sqlc.AgentLifecycleOperation {
	cloned := make(map[uuid.UUID][]sqlc.AgentLifecycleOperation, len(source))
	for clusterID, operations := range source {
		cloned[clusterID] = append([]sqlc.AgentLifecycleOperation(nil), operations...)
	}
	return cloned
}

func fakeClusterAgentRunTx(q *fakeClusterAgentQuerier) clusterAgentRunTxFunc {
	return func(_ context.Context, fn func(ClusterAgentMutationTx) error) error {
		created := append([]sqlc.AgentLifecycleOperation(nil), q.created...)
		idempotent := append([]sqlc.CreateAgentLifecycleOperationIdempotentParams(nil), q.idempotent...)
		operations := cloneAgentOperations(q.operations)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		if err := fn(q); err != nil {
			q.created, q.idempotent, q.operations, q.audits = created, idempotent, operations, audits
			return err
		}
		return nil
	}
}

func transactionalAgentUpgradeFixture(t *testing.T, auditErr error) (*ClusterAgentHandler, *fakeClusterAgentQuerier, *events.Bus, *http.Request) {
	t.Helper()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	clusterID := uuid.New()
	q := &fakeClusterAgentQuerier{
		clusters: []sqlc.Cluster{{
			ID: clusterID, Name: "prod", DisplayName: "Production", Status: "active",
			Annotations: profileAnnotation(agenttemplate.PrivilegeProfileOperator),
		}},
		history: map[uuid.UUID][]sqlc.AgentConnection{
			clusterID: {{
				ID: uuid.New(), ClusterID: clusterID, AgentID: "agent-prod", SessionID: "session-prod",
				Status: "connected", ConnectedAt: now.Add(-20 * time.Minute), LastPing: ts(now.Add(-20 * time.Second)), AgentVersion: "v1.1.0",
			}},
		},
		operations: map[uuid.UUID][]sqlc.AgentLifecycleOperation{},
		outboxErr:  auditErr,
	}
	bus := events.NewBus()
	h := NewClusterAgentHandler(q)
	h.now = func() time.Time { return now }
	h.SetAgentUpgradeTarget("private.example/astronomer-agent", "v1.2.3")
	h.SetEventBus(bus)
	h.SetRunTx(fakeClusterAgentRunTx(q))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster_id", clusterID.String())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cluster-agents/"+clusterID.String()+"/upgrade/", nil)
	req.Header.Set("Idempotency-Key", "transactional-agent-upgrade")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return h, q, bus, req
}

func TestClusterAgentUpgradeCommitsOperationAndAuditBeforePublishing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantStatus int
		wantCommit int
	}{
		{name: "commit", wantStatus: http.StatusAccepted, wantCommit: 1},
		{name: "audit failure rollback", auditErr: errors.New("audit-SENTINEL"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, q, bus, req := transactionalAgentUpgradeFixture(t, tc.auditErr)
			ch := pubSubscribe(t, bus)
			w := httptest.NewRecorder()
			h.Upgrade(w, req)
			if w.Code != tc.wantStatus || strings.Contains(w.Body.String(), "audit-SENTINEL") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(q.created) != tc.wantCommit || len(q.idempotent) != tc.wantCommit || len(q.audits) != tc.wantCommit {
				t.Fatalf("effects operations=%d idempotency=%d audits=%d, want %d", len(q.created), len(q.idempotent), len(q.audits), tc.wantCommit)
			}
			if tc.wantCommit == 0 {
				select {
				case event := <-ch:
					t.Fatalf("rollback published event: %+v", event)
				default:
				}
				if !strings.Contains(w.Body.String(), "audit_unavailable") {
					t.Fatalf("rollback did not fail closed: %s", w.Body.String())
				}
				return
			}
			event := pubReceive(t, ch, events.TypeClusterAgentChanged)
			if event.ID == 0 {
				t.Fatal("committed upgrade did not publish lifecycle event")
			}
			auditJSON := string(q.audits[0].Detail)
			if strings.Contains(auditJSON, "private.example") || strings.Contains(auditJSON, "target_image") {
				t.Fatalf("audit exposed agent image location: %s", auditJSON)
			}
		})
	}
}

func TestClusterAgentUpgradeContainsTransactionalAuditBoundary(t *testing.T) {
	path, err := filepath.Abs("cluster_agents.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundRunTx, foundOutbox := false, false
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Upgrade" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel.Name == "runTx" {
					foundRunTx = true
				}
			case *ast.Ident:
				if fun.Name == "recordAuditOutbox" {
					foundOutbox = true
				}
			}
			return true
		})
	}
	if !foundRunTx || !foundOutbox {
		t.Fatalf("Upgrade transaction boundary runTx=%v outbox=%v", foundRunTx, foundOutbox)
	}
}
