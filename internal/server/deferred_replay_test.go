package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
)

type deferredReplayQueryFake struct {
	installed sqlc.InstalledChart
}

func (f deferredReplayQueryFake) GetInstalledChartByID(_ context.Context, id uuid.UUID) (sqlc.InstalledChart, error) {
	if f.installed.ID != id {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	return f.installed, nil
}

func deferredReplayFixture(t *testing.T, operationType string, spec handler.DeferredOpSpec, clusterID, projectID, userID uuid.UUID) (sqlc.DeferredOperation, *auth.Encryptor, *auth.JWTManager) {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Encrypt(string(plain))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(handler.EncryptedDeferredOpSpec{SchemaVersion: 1, Ciphertext: sealed})
	if err != nil {
		t.Fatal(err)
	}
	row := sqlc.DeferredOperation{
		ID: uuid.New(), OperationType: operationType, OperationSpec: envelope,
		RequestedBy: pgtype.UUID{Bytes: userID, Valid: true},
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
	if clusterID != uuid.Nil {
		row.TargetClusterID = pgtype.UUID{Bytes: clusterID, Valid: true}
	}
	if projectID != uuid.Nil {
		row.TargetProjectID = pgtype.UUID{Bytes: projectID, Valid: true}
	}
	return row, cipher, auth.MustNewJWTManager("test-secret-key-for-deferred-replay", 60)
}

func TestReplayDeferredHTTPRequestReauthorizesThroughAPI(t *testing.T) {
	clusterID, userID := uuid.New(), uuid.New()
	row, cipher, jwtManager := deferredReplayFixture(t, maintenance.OpClusterDelete, handler.DeferredOpSpec{
		Method: http.MethodDelete, Path: "/api/v1/clusters/" + clusterID.String() + "/",
		QueryParams: map[string]string{"force": "true"},
	}, clusterID, uuid.Nil, userID)

	called := false
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/clusters/"+clusterID.String()+"/" || r.URL.Query().Get("force") != "true" {
			t.Fatalf("unexpected replay request: %s %s", r.Method, r.URL.String())
		}
		claims, err := jwtManager.ValidateToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if err != nil || claims.UserID != userID {
			t.Fatalf("replay credential does not represent the original user: claims=%+v err=%v", claims, err)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "deferred-replay:"+row.ID.String() {
			t.Fatalf("idempotency key = %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
	})
	if err := replayDeferredHTTPRequest(context.Background(), router, jwtManager, cipher, deferredReplayQueryFake{}, row); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("API router was not called")
	}
}

func TestReplayDeferredHTTPRequestRejectsTargetBodyMismatch(t *testing.T) {
	clusterID := uuid.New()
	body, _ := json.Marshal(map[string]string{"cluster_id": uuid.NewString()})
	row, cipher, jwtManager := deferredReplayFixture(t, maintenance.OpToolInstall, handler.DeferredOpSpec{
		Method: http.MethodPost, Path: "/api/v1/tools/cert-manager/install/", Body: body,
	}, clusterID, uuid.Nil, uuid.New())
	called := false
	err := replayDeferredHTTPRequest(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}), jwtManager, cipher, deferredReplayQueryFake{}, row)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected target mismatch, got %v", err)
	}
	if called {
		t.Fatal("invalid replay reached the API router")
	}
}

func TestReplayDeferredHTTPRequestChecksHelmInstallationCluster(t *testing.T) {
	clusterID, installationID := uuid.New(), uuid.New()
	row, cipher, jwtManager := deferredReplayFixture(t, maintenance.OpHelmUninstall, handler.DeferredOpSpec{
		Method: http.MethodDelete, Path: "/api/v1/catalog/installed/" + installationID.String() + "/",
	}, clusterID, uuid.Nil, uuid.New())
	queries := deferredReplayQueryFake{installed: sqlc.InstalledChart{ID: installationID, ClusterID: uuid.New()}}
	err := replayDeferredHTTPRequest(context.Background(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("mismatched installation reached the API router")
	}), jwtManager, cipher, queries, row)
	if err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("expected installation ownership error, got %v", err)
	}
}
