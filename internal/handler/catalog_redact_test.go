package handler

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestRedactHelmRepository_StripsSecrets(t *testing.T) {
	repo := sqlc.HelmRepository{
		Name:       "private",
		Url:        "https://charts.example.com",
		AuthType:   "basic",
		AuthConfig: json.RawMessage(`{"username":"u","password":"s3cret","token":"tok"}`),
	}
	out := redactHelmRepository(repo)
	var m map[string]any
	if err := json.Unmarshal(out.AuthConfig, &m); err != nil {
		t.Fatal(err)
	}
	if m["username"] != "u" {
		t.Fatalf("username should remain: %v", m["username"])
	}
	if m["password"] != SecretSentinel {
		t.Fatalf("password want sentinel got %v", m["password"])
	}
	if m["token"] != SecretSentinel {
		t.Fatalf("token want sentinel got %v", m["token"])
	}
	// Original must be unchanged for server-side use.
	var orig map[string]any
	_ = json.Unmarshal(repo.AuthConfig, &orig)
	if orig["password"] != "s3cret" {
		t.Fatal("redact must not mutate the source AuthConfig")
	}
}

func TestMergeAuthConfigPreservingSentinel(t *testing.T) {
	existing := json.RawMessage(`{"username":"u","password":"real"}`)
	incoming := json.RawMessage(`{"username":"u","password":"<encrypted>"}`)
	merged := mergeAuthConfigPreservingSentinel(existing, incoming)
	var m map[string]any
	if err := json.Unmarshal(merged, &m); err != nil {
		t.Fatal(err)
	}
	if m["password"] != "real" {
		t.Fatalf("sentinel should keep existing password, got %v", m["password"])
	}
}

func TestMergeAuthConfig_NewPasswordReplaces(t *testing.T) {
	existing := json.RawMessage(`{"password":"old"}`)
	incoming := json.RawMessage(`{"password":"new"}`)
	merged := mergeAuthConfigPreservingSentinel(existing, incoming)
	var m map[string]any
	_ = json.Unmarshal(merged, &m)
	if m["password"] != "new" {
		t.Fatalf("got %v", m["password"])
	}
}
