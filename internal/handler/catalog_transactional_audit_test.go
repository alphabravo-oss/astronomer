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
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type catalogSyncPreflightFake struct {
	CatalogQuerier
	repo sqlc.HelmRepository
}

func (q *catalogSyncPreflightFake) GetHelmRepositoryByID(context.Context, uuid.UUID) (sqlc.HelmRepository, error) {
	return q.repo, nil
}

type catalogMandatoryAuditFake struct {
	CatalogQuerier
	err error
}

func (q *catalogMandatoryAuditFake) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	return q.err
}

type stagedCatalogMutationTx struct {
	CatalogMutationTx
	fakeOperationIdempotencyStore
	install  sqlc.InstalledChart
	op       sqlc.CatalogOperation
	idemOp   sqlc.CatalogOperation
	audits   []sqlc.UpsertAuditOutboxParams
	tasks    []sqlc.UpsertTaskOutboxParams
	taskErr  error
	auditErr error
}

func (tx *stagedCatalogMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Status: "pending"}, nil
}

func (tx *stagedCatalogMutationTx) CreateInstalledChart(_ context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error) {
	tx.install = sqlc.InstalledChart{ID: uuid.New(), ClusterID: arg.ClusterID, ReleaseName: arg.ReleaseName, Namespace: arg.Namespace, Status: arg.Status}
	return tx.install, nil
}

func (tx *stagedCatalogMutationTx) CreateCatalogOperation(_ context.Context, arg sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error) {
	tx.op = sqlc.CatalogOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType, Status: arg.Status}
	return tx.op, nil
}

func (tx *stagedCatalogMutationTx) CreateCatalogOperationIdempotent(ctx context.Context, arg sqlc.CreateCatalogOperationIdempotentParams) (sqlc.CatalogOperation, error) {
	if tx.idemOp.ID != uuid.Nil {
		return tx.idemOp, nil
	}
	return tx.CreateCatalogOperation(ctx, sqlc.CreateCatalogOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
}

func (tx *stagedCatalogMutationTx) CreateCatalogOperationIdempotentWithDisposition(ctx context.Context, arg sqlc.CreateCatalogOperationIdempotentWithDispositionParams) (sqlc.CreateCatalogOperationIdempotentWithDispositionRow, error) {
	if tx.idemOp.ID != uuid.Nil {
		return sqlc.CreateCatalogOperationIdempotentWithDispositionRow{CatalogOperation: tx.idemOp, Inserted: false}, nil
	}
	op, err := tx.CreateCatalogOperation(ctx, sqlc.CreateCatalogOperationParams{
		TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType,
		Payload: arg.Payload, Status: arg.Status, CreatedByID: arg.CreatedByID,
	})
	return sqlc.CreateCatalogOperationIdempotentWithDispositionRow{CatalogOperation: op, Inserted: err == nil}, err
}

func (tx *stagedCatalogMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestCatalogStateOperationAndAuditCommitTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantErr    bool
		wantCommit int
	}{
		{name: "commit all", wantCommit: 1},
		{name: "audit failure rolls back state and operation", auditErr: errors.New("audit unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			committedState, committedOperations, committedAudits := 0, 0, 0
			h := NewCatalogHandler(nil)
			h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error {
				tx := &stagedCatalogMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedState++
				if tx.op.ID != uuid.Nil {
					committedOperations++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/c/installations/", nil)
			params := sqlc.CreateInstalledChartParams{ClusterID: uuid.New(), ReleaseName: "payments", Namespace: "apps", Status: "pending_install"}

			_, err := executeCatalogMutation(r, h,
				func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
					row, mutationErr := q.CreateInstalledChart(r.Context(), params)
					if mutationErr != nil {
						return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
					}
					op, mutationErr := createCatalogOperation(r.Context(), q, "installed_chart", row.ID.String(), "install", catalogOperationEnvelope{InstalledChartID: row.ID.String(), ClusterID: row.ClusterID.String()}, sqlc.CreateCatalogOperationParams{}.CreatedByID)
					return catalogMutationResult[sqlc.InstalledChart]{row: row, op: op}, mutationErr
				},
				func() (catalogMutationResult[sqlc.InstalledChart], error) {
					t.Fatal("production transaction unexpectedly used fallback")
					return catalogMutationResult[sqlc.InstalledChart]{}, nil
				},
				func(result catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
					return clusterAuditEvent{action: "catalog.installation.create", resourceType: "installed_chart", resourceID: result.row.ID.String(), status: http.StatusAccepted, detail: map[string]any{"operation_id": result.op.ID.String()}}
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
			if committedState != tc.wantCommit || committedOperations != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed state/operation/audit = %d/%d/%d, want %d each", committedState, committedOperations, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestCatalogSyncCommitsTaskAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		taskErr    error
		auditErr   error
		wantStatus int
		wantCommit int
	}{
		{name: "accepted", wantStatus: http.StatusAccepted, wantCommit: 1},
		{name: "task failure rolls back", taskErr: errors.New("task outbox unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "audit failure rolls back", auditErr: errors.New("audit outbox unavailable"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := sqlc.HelmRepository{ID: uuid.New(), Name: "platform", Url: "https://charts.example.com", RepoType: "helm"}
			h := NewCatalogHandler(&catalogSyncPreflightFake{repo: repo})
			committedTasks, committedAudits := 0, 0
			h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error {
				tx := &stagedCatalogMutationTx{taskErr: tc.taskErr, auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				for _, task := range tx.tasks {
					if strings.Contains(string(task.Payload), repo.Url) || !strings.Contains(string(task.Payload), repo.ID.String()) {
						t.Fatalf("task payload must use the stable repository ID and omit the potentially credential-bearing URL: %s", task.Payload)
					}
				}
				committedTasks += len(tx.tasks)
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/"+repo.ID.String()+"/sync/", nil)
			request.Header.Set("Idempotency-Key", "catalog-sync-test")
			routeContext := chi.NewRouteContext()
			routeContext.URLParams.Add("id", repo.ID.String())
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
			response := httptest.NewRecorder()

			h.SyncRepo(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			if committedTasks != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed task/audit = %d/%d, want %d each", committedTasks, committedAudits, tc.wantCommit)
			}
		})
	}
}

func TestCatalogSyncReplaysExactReceiptOnceUnderRace(t *testing.T) {
	repo := sqlc.HelmRepository{ID: uuid.New(), Name: "platform", Url: "https://charts.example.com", RepoType: "helm"}
	h := NewCatalogHandler(&catalogSyncPreflightFake{repo: repo})
	tx := &stagedCatalogMutationTx{}
	var transactionMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error {
		transactionMu.Lock()
		defer transactionMu.Unlock()
		return fn(tx)
	})
	router := chi.NewRouter()
	router.Post("/api/v1/catalog/repositories/{id}/sync/", h.SyncRepo)
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/"+repo.ID.String()+"/sync/", nil)
			req.Header.Set("Idempotency-Key", "catalog-sync-race")
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

func TestCatalogSyncKeyRejectsChangedTarget(t *testing.T) {
	repo := sqlc.HelmRepository{ID: uuid.New(), Name: "platform", RepoType: "helm"}
	h := NewCatalogHandler(&catalogSyncPreflightFake{repo: repo})
	tx := &stagedCatalogMutationTx{}
	h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error { return fn(tx) })
	router := chi.NewRouter()
	router.Post("/api/v1/catalog/repositories/{id}/sync/", h.SyncRepo)
	request := func(id uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/"+id.String()+"/sync/", nil)
		req.Header.Set("Idempotency-Key", "catalog-sync-conflict")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	first, changed := request(repo.ID), request(uuid.New())
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("changed target tasks/audits=%d/%d", len(tx.tasks), len(tx.audits))
	}
}

func TestCatalogCreateReplayCannotCommitOrphanInstallation(t *testing.T) {
	h := NewCatalogHandler(nil)
	commits := 0
	h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error {
		tx := &stagedCatalogMutationTx{idemOp: sqlc.CatalogOperation{
			ID: uuid.New(), TargetType: "installed_chart", TargetKey: uuid.NewString(),
			OperationType: "install", Payload: []byte(`{"clusterId":"prior"}`), Status: "pending",
		}}
		if err := fn(tx); err != nil {
			return err
		}
		commits++
		return nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/installed/", nil)
	request.Header.Set("Idempotency-Key", "install-payments-once")
	params := sqlc.CreateInstalledChartParams{ClusterID: uuid.New(), ReleaseName: "payments", Namespace: "apps", Status: "pending_install"}
	opCtx := withOperationIdempotency(request, "catalog")

	_, err := executeCatalogMutation(request, h,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			row, mutationErr := q.CreateInstalledChart(request.Context(), params)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", row.ID.String(), "install", catalogOperationEnvelope{InstalledChartID: row.ID.String(), ClusterID: row.ClusterID.String()}, sqlc.CreateCatalogOperationParams{}.CreatedByID)
			return catalogMutationResult[sqlc.InstalledChart]{row: row, op: op}, mutationErr
		},
		func() (catalogMutationResult[sqlc.InstalledChart], error) {
			return catalogMutationResult[sqlc.InstalledChart]{}, nil
		},
		func(result catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.installation.create", resourceType: "installed_chart", resourceID: result.row.ID.String(), status: http.StatusAccepted}
		})
	if !errors.Is(err, errCatalogOperationIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency conflict", err)
	}
	if commits != 0 {
		t.Fatal("a replay with an existing operation committed a second orphan installation")
	}
}

func TestCatalogConnectionResultFailsClosedWhenMandatoryAuditFails(t *testing.T) {
	q := &catalogMandatoryAuditFake{err: errors.New("postgres unavailable")}
	h := NewCatalogHandler(q)
	h.SetRunTx(func(context.Context, func(CatalogMutationTx) error) error { return nil })
	repo := sqlc.HelmRepository{ID: uuid.New(), Name: "private", RepoType: "oci"}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/repositories/"+repo.ID.String()+"/test-connection/", nil)
	response := httptest.NewRecorder()

	h.respondRepoConnectionResult(response, request, repo, http.StatusOK, true, "Connection successful.", http.StatusOK)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "Connection successful") {
		t.Fatalf("successful credential test result escaped without audit persistence: %s", response.Body.String())
	}
}

func TestCatalogRepositoryURLCannotCarrySecrets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https", raw: "https://charts.example.com/platform"},
		{name: "oci", raw: "oci://registry.example.com/platform"},
		{name: "scp style git", raw: "git@github.com:example/charts.git"},
		{name: "userinfo", raw: "https://user:secret@charts.example.com/platform", wantErr: true},
		{name: "query token", raw: "https://charts.example.com/platform?token=secret", wantErr: true},
		{name: "fragment", raw: "https://charts.example.com/platform#secret", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateCatalogRepositoryURL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestCatalogHighRiskMutationsUseTransactionalExecutor(t *testing.T) {
	path, err := filepath.Abs("catalog.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateRepo": false, "UpdateRepo": false, "DeleteRepo": false,
		"CreateInstallation": false, "DeleteInstallation": false,
		"UpgradeInstalledChart": false, "RollbackInstalledChart": false,
		"RetryOperation": false,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
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
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeCatalogMutation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s does not use executeCatalogMutation", name)
		}
	}
}
