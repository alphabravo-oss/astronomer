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
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type stagedGitOpsMutationTx struct {
	GitOpsMutationTx
	fakeOperationIdempotencyStore
	source   sqlc.GitopsRegistrationSource
	tasks    []sqlc.UpsertTaskOutboxParams
	audits   []sqlc.UpsertAuditOutboxParams
	taskErr  error
	auditErr error
}

func (tx *stagedGitOpsMutationTx) GetGitOpsSource(_ context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error) {
	tx.source = sqlc.GitopsRegistrationSource{ID: id, Name: "platform", RepoUrl: "https://git.example/platform"}
	return tx.source, nil
}

func (tx *stagedGitOpsMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (tx *stagedGitOpsMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestGitOpsSyncTaskAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit task and audit", wantCommit: 1},
		{name: "task failure rolls back", taskErr: errors.New("task outbox unavailable"), wantErr: true},
		{name: "audit failure rolls back task", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedTasks, committedAudits := 0, 0
			h := NewGitOpsHandler(nil, nil, nil)
			h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error {
				tx := &stagedGitOpsMutationTx{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			sourceID := uuid.New()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gitops-sources/"+sourceID.String()+"/sync/", nil)

			_, err := executeGitOpsMutation(r, h,
				func(q GitOpsMutationTx) (gitOpsSyncMutationResult, error) {
					return enqueueGitOpsSourceSync(r, q, q, sourceID)
				},
				func() (gitOpsSyncMutationResult, error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return gitOpsSyncMutationResult{}, nil
				},
				func(result gitOpsSyncMutationResult) clusterAuditEvent {
					return clusterAuditEvent{
						action: "admin.gitops_source.sync_requested", resourceType: "gitops_source",
						resourceID: result.row.ID.String(), resourceName: result.row.Name,
						status: http.StatusAccepted, detail: map[string]any{"task_type": tasks.GitOpsSyncType},
					}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed task/audit = %d/%d, want %d/%d", committedTasks, committedAudits, tc.wantCommit, tc.wantCommit)
			}
		})
	}
}

func TestGitOpsSyncReplaysExactReceiptOnceUnderRace(t *testing.T) {
	callerID, sourceID := uuid.New(), uuid.New()
	queries := newFakeHandlerQuerier()
	queries.user = sqlc.User{ID: callerID, IsActive: true, IsSuperuser: true}
	queries.sources[sourceID] = sqlc.GitopsRegistrationSource{ID: sourceID, Name: "platform", RepoUrl: "https://git.example/platform"}
	tx := &stagedGitOpsMutationTx{source: queries.sources[sourceID]}
	h := NewGitOpsHandler(queries, nil, nil)
	var transactionMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error {
		transactionMu.Lock()
		defer transactionMu.Unlock()
		return fn(tx)
	})
	router := chi.NewRouter()
	router.Post("/api/v1/admin/gitops-sources/{id}/sync/", h.Sync)
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/"+sourceID.String()+"/sync/", nil, callerID)
			req.Header.Set("Idempotency-Key", "gitops-sync-race")
			responses[index] = httptest.NewRecorder()
			router.ServeHTTP(responses[index], req)
		}(i)
	}
	wg.Wait()
	for _, response := range responses {
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if responses[0].Body.String() != responses[1].Body.String() || responses[0].Header().Get("Location") != responses[1].Header().Get("Location") {
		t.Fatalf("replay receipt changed: first=%s second=%s", responses[0].Body.String(), responses[1].Body.String())
	}
	if len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("racing replay tasks/audits=%d/%d, want 1/1", len(tx.tasks), len(tx.audits))
	}
}

func TestGitOpsSyncKeyRejectsChangedTarget(t *testing.T) {
	callerID, firstID, secondID := uuid.New(), uuid.New(), uuid.New()
	queries := newFakeHandlerQuerier()
	queries.user = sqlc.User{ID: callerID, IsActive: true, IsSuperuser: true}
	queries.sources[firstID] = sqlc.GitopsRegistrationSource{ID: firstID, Name: "first"}
	queries.sources[secondID] = sqlc.GitopsRegistrationSource{ID: secondID, Name: "second"}
	tx := &stagedGitOpsMutationTx{}
	h := NewGitOpsHandler(queries, nil, nil)
	h.SetRunTx(func(_ context.Context, fn func(GitOpsMutationTx) error) error { return fn(tx) })
	router := chi.NewRouter()
	router.Post("/api/v1/admin/gitops-sources/{id}/sync/", h.Sync)
	request := func(id uuid.UUID) *httptest.ResponseRecorder {
		req := gitopsAuthedRequest(http.MethodPost, "/api/v1/admin/gitops-sources/"+id.String()+"/sync/", nil, callerID)
		req.Header.Set("Idempotency-Key", "gitops-sync-conflict")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	first, changed := request(firstID), request(secondID)
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("changed target tasks/audits=%d/%d", len(tx.tasks), len(tx.audits))
	}
}

func TestEveryGitOpsMutationUsesTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("gitops.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Create": false, "Update": false, "Delete": false, "Webhook": false}
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeGitOpsMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeGitOpsMutation", name)
		}
	}
}
