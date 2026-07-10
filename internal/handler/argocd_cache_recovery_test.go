package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func syncByNameRequest(t *testing.T, instanceID uuid.UUID, name, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/typed", strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", instanceID.String())
	route.URLParams.Add("name", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
}

func TestSyncAppByNameRecoversEmptyCacheAndCreatesBoundOperation(t *testing.T) {
	var liveReads atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/applications/astronomer-self-manage" {
			t.Fatalf("unexpected upstream request %s %s", r.Method, r.URL.Path)
		}
		liveReads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"metadata":{"name":"astronomer-self-manage","uid":"self-manage-uid","namespace":"astronomer","annotations":{"credential":"must-not-persist"}},
			"spec":{"project":"default","source":{"repoURL":"https://user:pass@example.test/platform.git?token=must-not-persist#frag","path":"charts/astronomer","targetRevision":"main","helm":{"values":"password=must-not-persist"}},"destination":{"name":"in-cluster","namespace":"astronomer"}},
			"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Healthy"}}
		}`))
	})
	rec.app.Name = "astronomer-self-manage"
	rec.app.ID = uuid.New()
	req := syncByNameRequest(t, rec.instance.ID, rec.app.Name, `{"prune":false}`)
	rr := httptest.NewRecorder()

	h.SyncAppByName(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if liveReads.Load() != 1 || len(rec.upserts) != 1 || len(rec.created) != 1 {
		t.Fatalf("live=%d upserts=%d operations=%d", liveReads.Load(), len(rec.upserts), len(rec.created))
	}
	upsert := rec.upserts[0]
	if upsert.UpstreamUid != "self-manage-uid" || upsert.ApplicationNamespace != "astronomer" || upsert.Project != "default" || upsert.DestinationNamespace != "astronomer" {
		t.Fatalf("unsafe/incomplete projection: %+v", upsert)
	}
	projection, _ := json.Marshal(upsert)
	if strings.Contains(string(projection), "must-not-persist") || strings.Contains(string(projection), "user:pass") || strings.Contains(string(projection), "?token=") || strings.Contains(string(projection), "#frag") {
		t.Fatalf("projection persisted credential/spec material: %s", projection)
	}
	var env argocdOperationEnvelope
	if err := json.Unmarshal(rec.created[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.ApplicationID != rec.app.ID.String() || env.InstanceID != rec.instance.ID.String() || env.UpstreamUID != "self-manage-uid" {
		t.Fatalf("operation not bound to stable identities: %+v", env)
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "application/json") || !strings.Contains(rr.Body.String(), `"status":"pending"`) {
		t.Fatalf("unexpected accepted operation envelope: %s", rr.Body.String())
	}
}

func TestSyncAppByNameStaleInstanceFailsBeforeOperation(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"uid-1"}}`))
	})
	rec.upsertErr = &pgconn.PgError{Code: "23503"}
	rr := httptest.NewRecorder()
	h.SyncAppByName(rr, syncByNameRequest(t, rec.instance.ID, "myapp", `{}`))
	if rr.Code != http.StatusConflict || len(rec.created) != 0 {
		t.Fatalf("status=%d created=%d body=%s", rr.Code, len(rec.created), rr.Body.String())
	}
}

func TestSyncAppByNameRejectsConflictingCachedIncarnation(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"replacement-uid"}}`))
	})
	rec.upsertErr = pgx.ErrNoRows
	rr := httptest.NewRecorder()
	h.SyncAppByName(rr, syncByNameRequest(t, rec.instance.ID, "myapp", `{}`))
	if rr.Code != http.StatusConflict || len(rec.created) != 0 {
		t.Fatalf("status=%d created=%d body=%s", rr.Code, len(rec.created), rr.Body.String())
	}
}

func TestSyncAppByNameMissingInstanceNeverContactsUpstream(t *testing.T) {
	var calls atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	rec.getInstanceErr = pgx.ErrNoRows
	// Resolution fails before authorization and before any live request.
	rr := httptest.NewRecorder()
	h.SyncAppByName(rr, syncByNameRequest(t, uuid.Nil, "myapp", `{}`))
	if rr.Code != http.StatusNotFound || calls.Load() != 0 || len(rec.upserts) != 0 || len(rec.created) != 0 {
		t.Fatalf("status=%d upstream=%d upserts=%d operations=%d", rr.Code, calls.Load(), len(rec.upserts), len(rec.created))
	}
}

func TestSyncAppByNameAuthorizesBeforeUpstreamDiscovery(t *testing.T) {
	var calls atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: nil})
	req := syncByNameRequest(t, rec.instance.ID, "myapp", `{}`)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rr := httptest.NewRecorder()
	h.SyncAppByName(rr, req)
	if rr.Code != http.StatusForbidden || calls.Load() != 0 || len(rec.upserts) != 0 || len(rec.created) != 0 {
		t.Fatalf("status=%d upstream=%d upserts=%d operations=%d", rr.Code, calls.Load(), len(rec.upserts), len(rec.created))
	}
}

func TestSyncAppByNameRejectsPathInjectionBeforeUpstream(t *testing.T) {
	for _, name := range []string{"myapp/sync", "myapp?x=y", "myapp#frag", "..", " myapp", "myapp "} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
			rr := httptest.NewRecorder()
			h.SyncAppByName(rr, syncByNameRequest(t, rec.instance.ID, name, `{}`))
			if rr.Code != http.StatusBadRequest || calls.Load() != 0 || len(rec.upserts) != 0 || len(rec.created) != 0 {
				t.Fatalf("status=%d upstream=%d upserts=%d operations=%d", rr.Code, calls.Load(), len(rec.upserts), len(rec.created))
			}
		})
	}
}

func TestSyncAppByIDRejectsRecreatedApplicationWithoutRebindingStableID(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"replacement-uid"}}`))
	})
	req := argoHandlerRouteRequest(http.MethodPost, "/typed", `{}`, map[string]string{"id": rec.app.ID.String()})
	rr := httptest.NewRecorder()
	h.SyncApp(rr, req)
	if rr.Code != http.StatusConflict || len(rec.upserts) != 0 || len(rec.created) != 0 || rec.app.UpstreamUid != "upstream-myapp-uid" {
		t.Fatalf("status=%d upserts=%d operations=%d app=%+v", rr.Code, len(rec.upserts), len(rec.created), rec.app)
	}
}

func TestSyncAppByIDAdoptsUIDOnlyForLegacyEmptyIdentity(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"discovered-uid","namespace":"astronomer"}}`))
	})
	rec.app.UpstreamUid = ""
	req := argoHandlerRouteRequest(http.MethodPost, "/typed", `{}`, map[string]string{"id": rec.app.ID.String()})
	rr := httptest.NewRecorder()
	h.SyncApp(rr, req)
	if rr.Code != http.StatusAccepted || len(rec.upserts) != 1 || len(rec.created) != 1 || rec.app.UpstreamUid != "discovered-uid" {
		t.Fatalf("status=%d upserts=%d operations=%d uid=%q body=%s", rr.Code, len(rec.upserts), len(rec.created), rec.app.UpstreamUid, rr.Body.String())
	}
}

func TestSyncAppByNameConcurrentRecoveryUsesOneStableLocalID(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"upstream-myapp-uid","namespace":"astronomer"},"spec":{"project":"default"}}`))
	})
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.SyncAppByName(rr, syncByNameRequest(t, rec.instance.ID, "myapp", `{}`))
			if rr.Code != http.StatusAccepted {
				errs <- rr.Body.String()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent recovery failed: %s", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.upserts) != workers || len(rec.created) != workers {
		t.Fatalf("upserts=%d operations=%d", len(rec.upserts), len(rec.created))
	}
	for _, op := range rec.created {
		if op.TargetKey != rec.app.ID.String() {
			t.Fatalf("operation target drifted: %s want %s", op.TargetKey, rec.app.ID)
		}
	}
}

func TestExecuteSyncRejectsRecreatedSameNameBeforeMutation(t *testing.T) {
	var gets, posts atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets.Add(1)
			_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"replacement-uid"}}`))
			return
		}
		posts.Add(1)
	})
	op := sqlc.ArgocdOperation{
		ID: uuid.New(), OperationType: "sync", Status: "running",
		Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), UpstreamUID: rec.app.UpstreamUid}),
	}
	_, err := h.executeOperation(context.Background(), op)
	if err == nil || !strings.Contains(err.Error(), "identity changed") || gets.Load() != 1 || posts.Load() != 0 {
		t.Fatalf("err=%v gets=%d posts=%d", err, gets.Load(), posts.Load())
	}
}

func TestExecuteSyncRejectsUIDChangeInMutationResponse(t *testing.T) {
	var posts atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		uid := "upstream-myapp-uid"
		if r.Method == http.MethodPost {
			posts.Add(1)
			uid = "replacement-uid"
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"` + uid + `"},"status":{"operationState":{"phase":"Running"}}}`))
	})
	op := sqlc.ArgocdOperation{ID: uuid.New(), OperationType: "sync", Status: "running", Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), UpstreamUID: rec.app.UpstreamUid})}
	_, err := h.executeOperation(context.Background(), op)
	if err == nil || !strings.Contains(err.Error(), "identity changed") || posts.Load() != 1 || len(rec.appUpdate) != 0 {
		t.Fatalf("err=%v posts=%d updates=%d", err, posts.Load(), len(rec.appUpdate))
	}
}

func TestNormalizeSyncRequestRevisionBound(t *testing.T) {
	if _, err := normalizeSyncRequest(SyncRequest{Revision: strings.Repeat("a", 256)}); err != nil {
		t.Fatalf("256-byte revision rejected: %v", err)
	}
	if _, err := normalizeSyncRequest(SyncRequest{Revision: strings.Repeat("a", 257)}); err == nil {
		t.Fatal("257-byte revision accepted")
	}
}

func TestExecuteSyncRejectsCrossInstanceEnvelopeBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	op := sqlc.ArgocdOperation{
		ID: uuid.New(), OperationType: "sync", Status: "running",
		Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: uuid.NewString(), UpstreamUID: rec.app.UpstreamUid}),
	}
	_, err := h.executeOperation(context.Background(), op)
	if err == nil || !strings.Contains(err.Error(), "instance identity") || calls.Load() != 0 {
		t.Fatalf("err=%v upstream=%d", err, calls.Load())
	}
}

func TestRunningOperationMissingReferencesBecomesTerminal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		missingApp  bool
		missingInst bool
	}{
		{name: "application deleted", missingApp: true},
		{name: "instance deleted", missingInst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("upstream must not be called") })
			if tc.missingApp {
				rec.getAppErr = pgx.ErrNoRows
			}
			if tc.missingInst {
				rec.getInstanceErr = pgx.ErrNoRows
			}
			op := sqlc.ArgocdOperation{ID: uuid.New(), OperationType: "sync", Status: "running", Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), UpstreamUID: rec.app.UpstreamUid})}
			rec.injectRunning(op)
			h.pollRunningOperations(context.Background())
			if len(rec.failed) != 1 || rec.failed[0].Phase != "Failed" {
				t.Fatalf("failed=%+v", rec.failed)
			}
		})
	}
}

func TestRunningOperationCrossInstanceEnvelopeFailsBeforeUpstream(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("upstream must not be called") })
	op := sqlc.ArgocdOperation{ID: uuid.New(), OperationType: "sync", Status: "running", Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: uuid.NewString(), UpstreamUID: rec.app.UpstreamUid})}
	rec.injectRunning(op)
	h.pollRunningOperations(context.Background())
	if len(rec.failed) != 1 || !strings.Contains(rec.failed[0].Message, "instance identity") {
		t.Fatalf("failed=%+v", rec.failed)
	}
}

func TestRunningOperationRejectsRecreatedSameNameUID(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"name":"myapp","uid":"replacement-uid"},"status":{"operationState":{"phase":"Running"}}}`))
	})
	op := sqlc.ArgocdOperation{ID: uuid.New(), OperationType: "sync", Status: "running", Payload: mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), UpstreamUID: rec.app.UpstreamUid})}
	rec.injectRunning(op)
	h.pollRunningOperations(context.Background())
	if len(rec.failed) != 1 || !strings.Contains(rec.failed[0].Message, "identity changed") || len(rec.progress) != 0 {
		t.Fatalf("failed=%+v progress=%+v", rec.failed, rec.progress)
	}
}

func TestGetArgoCDOperation404IsNeverCompletion(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) {})
	id := uuid.New()
	rec.getOpErr = pgx.ErrNoRows
	rr := httptest.NewRecorder()
	h.GetOperation(rr, argoHandlerRouteRequest(http.MethodGet, "/typed", "", map[string]string{"id": id.String()}))
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "completed") {
		t.Fatalf("404 must remain an explicit missing-row error: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rec.getOpErr = nil
	rec.operation = sqlc.ArgocdOperation{ID: id, TargetType: "application", TargetKey: rec.app.ID.String(), OperationType: "sync", Status: "completed", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	rr = httptest.NewRecorder()
	h.GetOperation(rr, argoHandlerRouteRequest(http.MethodGet, "/typed", "", map[string]string{"id": id.String()}))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"completed"`) {
		t.Fatalf("terminal operation must remain pollable: status=%d body=%s", rr.Code, rr.Body.String())
	}
}
