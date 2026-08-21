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
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type recordingLokiReconciler struct {
	calls int
	err   error
}

func (r *recordingLokiReconciler) ReconcileLokiIngest(context.Context) error {
	if r == nil {
		return nil
	}
	r.calls++
	return r.err
}

func TestRotateOutputTokenMintsHashAndFernet(t *testing.T) {
	clusterID := uuid.New()
	q := newLoggingFakeQuerier()
	out, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          "astronomer-logs",
		OutputType:    "loki",
		Configuration: json.RawMessage(`{"host":"loki-ingest.example.com"}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
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
	recReconcile := &recordingLokiReconciler{}
	h.SetLokiIngestReconciler(recReconcile)
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbUpdate)}}},
	}}})

	req := authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/"+out.ID.String()+"/rotate-token/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", out.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	// Restore authenticated user after WithContext replacement of the chi ctx only.
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))

	w := httptest.NewRecorder()
	h.RotateOutputToken(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var wrap struct {
		Data struct {
			ClusterID string `json:"clusterId"`
			Token     string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrap.Data.ClusterID != clusterID.String() || wrap.Data.Token == "" {
		t.Fatalf("response = %+v", wrap.Data)
	}
	if strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), "token_encrypted") {
		t.Fatal("response leaked hash or ciphertext")
	}
	stored := q.lokiTokens[clusterID]
	if stored.TokenHash != lokiauth.HashBearer(wrap.Data.Token) {
		t.Fatalf("stored hash does not match minted token")
	}
	plain, err := enc.Decrypt(stored.TokenEncrypted)
	if err != nil {
		t.Fatalf("decrypt stored token: %v", err)
	}
	if plain != wrap.Data.Token {
		t.Fatal("Fernet round-trip mismatch")
	}
	if recReconcile.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", recReconcile.calls)
	}
	if len(q.operations) != 1 {
		t.Fatalf("rotate enqueued %d member apply ops, want 1", len(q.operations))
	}
}

func TestRotateOutputTokenDeniesZeroGrant(t *testing.T) {
	clusterID := uuid.New()
	q := newLoggingFakeQuerier()
	out, _ := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          "astronomer-logs",
		OutputType:    "loki",
		Configuration: json.RawMessage(`{}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
	})
	h := NewLoggingHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: nil})
	req := authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/"+out.ID.String()+"/rotate-token/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", out.ID.String())
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(
		context.WithValue(req.Context(), chi.RouteCtxKey, rc),
		&middleware.AuthenticatedUser{ID: uuid.NewString()},
	))
	w := httptest.NewRecorder()
	h.RotateOutputToken(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}
