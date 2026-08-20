package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type stubLokiAttachGate struct {
	state    lokiAttachState
	code     string
	msg      string
	ok       bool
	capCalls int
}

func (s *stubLokiAttachGate) LokiAttachState(context.Context) lokiAttachState {
	if s == nil {
		return lokiAttachState{}
	}
	return s.state
}

func (s *stubLokiAttachGate) CheckLokiAttachCapacity(context.Context, uuid.UUID) (string, string, bool) {
	if s == nil {
		return apierror.LokiNotReady, "gate not configured", false
	}
	s.capCalls++
	return s.code, s.msg, s.ok
}

func healthyPublicAttachGate() *stubLokiAttachGate {
	return &stubLokiAttachGate{
		state: lokiAttachState{
			Status:       "healthy",
			IngestPublic: true,
			Host:         "loki-ingest.example.com",
			Port:         "443",
			Mode:         sizerModeSingleBinary,
		},
		ok: true,
	}
}

func attachAstronomerReq(t *testing.T, clusterID uuid.UUID, rotate bool, bindings []rbac.RoleBinding) (*LoggingHandler, *loggingFakeQuerier, *recordingLokiReconciler, *http.Request) {
	t.Helper()
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEncryptor(enc)
	rec := &recordingLokiReconciler{}
	h.SetLokiIngestReconciler(rec)
	h.SetLokiAttachGate(healthyPublicAttachGate())
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: bindings})

	target := "/api/v1/clusters/" + clusterID.String() + "/logging/outputs/attach-astronomer/"
	if rotate {
		target += "?rotate=true"
	}
	req := authedLoggingReq(http.MethodPost, target, nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", clusterID.String())
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(
		context.WithValue(req.Context(), chi.RouteCtxKey, rc),
		&middleware.AuthenticatedUser{ID: uuid.NewString()},
	))
	return h, q, rec, req
}

func loggingCreateBinding(clusterID uuid.UUID) []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbCreate)}}},
	}}
}

func TestAttachAstronomerLogsCreated(t *testing.T) {
	clusterID := uuid.New()
	h, q, rec, req := attachAstronomerReq(t, clusterID, false, loggingCreateBinding(clusterID))
	w := httptest.NewRecorder()
	h.AttachAstronomerLogs(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data struct {
			Name       string          `json:"name"`
			OutputType string          `json:"output_type"`
			IsSystem   bool            `json:"is_system"`
			Enabled    bool            `json:"enabled"`
			Token      string          `json:"token"`
			Config     json.RawMessage `json:"configuration"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrap.Data.Name != systemLoggingOutputName || wrap.Data.OutputType != "loki" || !wrap.Data.IsSystem || !wrap.Data.Enabled {
		t.Fatalf("output = %+v", wrap.Data)
	}
	if wrap.Data.Token == "" {
		t.Fatal("201 body missing plaintext token")
	}
	if strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), "token_encrypted") || strings.Contains(w.Body.String(), "bearer") {
		t.Fatal("response leaked hash, ciphertext, or bearer")
	}
	stored := q.lokiTokens[clusterID]
	if stored.TokenHash != lokiauth.HashBearer(wrap.Data.Token) {
		t.Fatal("stored hash does not match minted token")
	}
	if rec.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", rec.calls)
	}
	if len(q.operations) != 1 {
		t.Fatalf("enqueued operations = %d, want 1", len(q.operations))
	}
	out, err := q.GetSystemLoggingOutputByCluster(context.Background(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil || !out.IsSystem || !out.Enabled {
		t.Fatalf("system row = %+v err=%v", out, err)
	}
}

func TestAttachAstronomerLogsIdempotent(t *testing.T) {
	clusterID := uuid.New()
	h, q, rec, req := attachAstronomerReq(t, clusterID, false, loggingCreateBinding(clusterID))
	first := httptest.NewRecorder()
	h.AttachAstronomerLogs(first, req)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
	}
	var firstWrap struct {
		Data struct {
			Token string `json:"token"`
			ID    string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstWrap); err != nil {
		t.Fatal(err)
	}
	firstToken := firstWrap.Data.Token
	firstHash := q.lokiTokens[clusterID].TokenHash

	secondReq := authedLoggingReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/logging/outputs/attach-astronomer/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", clusterID.String())
	secondReq = secondReq.WithContext(middleware.SetAuthenticatedUserForTest(
		context.WithValue(secondReq.Context(), chi.RouteCtxKey, rc),
		&middleware.AuthenticatedUser{ID: uuid.NewString()},
	))
	second := httptest.NewRecorder()
	h.AttachAstronomerLogs(second, secondReq)
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent status = %d, want 200: %s", second.Code, second.Body.String())
	}
	if strings.Contains(second.Body.String(), `"token"`) {
		t.Fatal("idempotent response must not re-issue plaintext token")
	}
	if q.lokiTokens[clusterID].TokenHash != firstHash {
		t.Fatal("idempotent attach rotated the token")
	}
	if rec.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1 (no second reconcile)", rec.calls)
	}
	if n := len(q.outputs); n != 1 {
		t.Fatalf("outputs = %d, want 1", n)
	}
	_ = firstToken
}

func TestAttachAstronomerLogsRotate(t *testing.T) {
	clusterID := uuid.New()
	h, q, _, req := attachAstronomerReq(t, clusterID, false, loggingCreateBinding(clusterID))
	first := httptest.NewRecorder()
	h.AttachAstronomerLogs(first, req)
	oldHash := q.lokiTokens[clusterID].TokenHash

	rotateReq := authedLoggingReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/logging/outputs/attach-astronomer/?rotate=true", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", clusterID.String())
	rotateReq = rotateReq.WithContext(middleware.SetAuthenticatedUserForTest(
		context.WithValue(rotateReq.Context(), chi.RouteCtxKey, rc),
		&middleware.AuthenticatedUser{ID: uuid.NewString()},
	))
	rotate := httptest.NewRecorder()
	h.AttachAstronomerLogs(rotate, rotateReq)
	if rotate.Code != http.StatusCreated {
		t.Fatalf("rotate status = %d: %s", rotate.Code, rotate.Body.String())
	}
	if q.lokiTokens[clusterID].TokenHash == oldHash {
		t.Fatal("rotate=true did not mint a new token")
	}
	if !strings.Contains(rotate.Body.String(), `"token"`) {
		t.Fatal("rotate response missing plaintext token")
	}
}

func TestAttachAstronomerLogsFreeze409(t *testing.T) {
	clusterID := uuid.New()
	h, _, _, req := attachAstronomerReq(t, clusterID, false, loggingCreateBinding(clusterID))
	h.SetLokiAttachGate(&stubLokiAttachGate{
		state: lokiAttachState{Status: "degraded_capacity", IngestPublic: true, Host: "loki-ingest.example.com"},
		ok:    true,
	})
	w := httptest.NewRecorder()
	h.AttachAstronomerLogs(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), apierror.DegradedCapacity) {
		t.Fatalf("body = %s, want %s", w.Body.String(), apierror.DegradedCapacity)
	}
}

func TestAttachAstronomerLogsCap409(t *testing.T) {
	clusterID := uuid.New()
	h, _, _, req := attachAstronomerReq(t, clusterID, false, loggingCreateBinding(clusterID))
	h.SetLokiAttachGate(&stubLokiAttachGate{
		state: lokiAttachState{Status: "healthy", IngestPublic: true, Host: "loki-ingest.example.com"},
		code:  apierror.IngestCapExceeded,
		msg:   "running mode singleBinary cannot absorb another cluster",
		ok:    false,
	})
	w := httptest.NewRecorder()
	h.AttachAstronomerLogs(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), apierror.IngestCapExceeded) {
		t.Fatalf("body = %s, want %s", w.Body.String(), apierror.IngestCapExceeded)
	}
}

func TestAttachAstronomerLogsForbiddenWithoutCreate(t *testing.T) {
	clusterID := uuid.New()
	h, _, _, req := attachAstronomerReq(t, clusterID, false, nil)
	w := httptest.NewRecorder()
	h.AttachAstronomerLogs(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}

	readOnly := []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead), string(rbac.VerbList)}}},
	}}
	h2, _, _, req2 := attachAstronomerReq(t, clusterID, false, readOnly)
	w2 := httptest.NewRecorder()
	h2.AttachAstronomerLogs(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("read-only status = %d, want 403: %s", w2.Code, w2.Body.String())
	}
}

func TestGetAstronomerAttachStatus(t *testing.T) {
	clusterID := uuid.New()
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	h.SetLokiAttachGate(healthyPublicAttachGate())
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead)}}},
	}}})
	req := authedLoggingReq(http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/logging/outputs/attach-astronomer/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", clusterID.String())
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(
		context.WithValue(req.Context(), chi.RouteCtxKey, rc),
		&middleware.AuthenticatedUser{ID: uuid.NewString()},
	))
	w := httptest.NewRecorder()
	h.GetAstronomerAttachStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ingestPublic":true`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"attached":false`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}
