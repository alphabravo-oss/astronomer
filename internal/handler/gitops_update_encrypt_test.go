package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// TestGitOpsUpdate_EncryptsNewAuthAtRest is the regression for the
// "Update stores new git auth token in plaintext" bug. When an encryptor is
// wired and a PUT supplies a fresh (non-sentinel) credential, the value
// written to auth_encrypted must be Fernet ciphertext that decrypts back to
// the supplied cleartext — never the cleartext itself.
func TestGitOpsUpdate_EncryptsNewAuthAtRest(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	id := uuid.New()
	q.sources[id] = sqlc.GitopsRegistrationSource{
		ID:                  id,
		Name:                "demo",
		RepoUrl:             "https://example/demo",
		Branch:              "main",
		AuthMode:            "https_token",
		AuthEncrypted:       "original-secret",
		SyncMode:            "interval",
		SyncIntervalSeconds: 60,
		OnDelete:            "log",
		Enabled:             true,
	}
	enc, err := auth.NewEncryptor(mustKey(t))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	h := NewGitOpsHandler(q, &fakeRunner{}, nil)
	h.SetEncryptor(enc)

	const freshPAT = "ghp_rotatedTokenValue123"
	body := []byte(`{
		"name": "demo",
		"repo_url": "https://example/demo",
		"auth_mode": "https_token",
		"auth": "` + freshPAT + `",
		"sync_mode": "interval",
		"sync_interval_seconds": 60,
		"on_delete": "log"
	}`)
	r := chi.NewRouter()
	r.Put("/api/v1/admin/gitops-sources/{id}/", h.Update)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodPut, "/api/v1/admin/gitops-sources/"+id.String()+"/", body, callerID)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", w.Code, w.Body.String())
	}

	stored := q.sources[id].AuthEncrypted
	if stored == freshPAT {
		t.Fatalf("auth stored in PLAINTEXT on update; expected Fernet ciphertext")
	}
	if stored == "" {
		t.Fatalf("auth blob unexpectedly empty")
	}
	plain, derr := enc.Decrypt(stored)
	if derr != nil {
		t.Fatalf("stored auth is not valid Fernet ciphertext: %v", derr)
	}
	if plain != freshPAT {
		t.Fatalf("decrypted auth = %q, want %q", plain, freshPAT)
	}
}

// TestGitOpsUpdate_SentinelStillPreservesWithEncryptor confirms the fix did
// not break the preserve-on-sentinel behavior: echoing the sentinel keeps
// the stored blob untouched even when an encryptor is wired.
func TestGitOpsUpdate_SentinelStillPreservesWithEncryptor(t *testing.T) {
	callerID := uuid.New()
	q := newFakeHandlerQuerier()
	q.user = sqlc.User{ID: callerID, IsSuperuser: true}
	id := uuid.New()
	q.sources[id] = sqlc.GitopsRegistrationSource{
		ID:                  id,
		Name:                "demo",
		RepoUrl:             "https://example/demo",
		Branch:              "main",
		AuthMode:            "https_token",
		AuthEncrypted:       "already-ciphertext",
		SyncMode:            "interval",
		SyncIntervalSeconds: 60,
		OnDelete:            "log",
		Enabled:             true,
	}
	enc, err := auth.NewEncryptor(mustKey(t))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	h := NewGitOpsHandler(q, &fakeRunner{}, nil)
	h.SetEncryptor(enc)

	body := []byte(`{
		"name": "demo",
		"repo_url": "https://example/demo",
		"auth_mode": "https_token",
		"auth": "` + GitOpsAuthSentinel + `",
		"sync_mode": "interval",
		"sync_interval_seconds": 60,
		"on_delete": "tombstone"
	}`)
	r := chi.NewRouter()
	r.Put("/api/v1/admin/gitops-sources/{id}/", h.Update)
	w := httptest.NewRecorder()
	req := gitopsAuthedRequest(http.MethodPut, "/api/v1/admin/gitops-sources/"+id.String()+"/", body, callerID)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := q.sources[id].AuthEncrypted; got != "already-ciphertext" {
		t.Fatalf("sentinel must preserve stored blob; got %q", got)
	}
}

func mustKey(t *testing.T) string {
	t.Helper()
	k, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}
