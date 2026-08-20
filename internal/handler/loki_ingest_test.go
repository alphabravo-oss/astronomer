package handler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/lokiauth"
	"github.com/google/uuid"
)

func TestReconcileLokiIngestWritesHashSecretWithoutPlaintext(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	fake := &loggingFakeRequester{}
	h.requester = fake
	trueVal := true
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		StorageConfigID:     stackTestStorageID,
		IngestHostname:      "loki-ingest.example.com",
		SkipDiskCheck:       &trueVal,
	}, "healthy"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	plaintext := "never-project-this"
	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: lokiauth.HashBearer(plaintext),
	}}

	if err := h.ReconcileLokiIngest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var sawSecret, sawIngress, sawACL bool
	for _, call := range fake.calls {
		body := string(call.body)
		if strings.Contains(body, plaintext) || strings.Contains(strings.ToLower(body), "never-project-this") {
			t.Fatalf("plaintext leaked onto cluster: %s %s %s", call.method, call.path, body)
		}
		if strings.Contains(call.path, "secrets/"+lokiTokenHashSecretName) || (call.method == "POST" && strings.Contains(call.path, "/secrets") && strings.Contains(body, lokiTokenHashSecretName)) {
			sawSecret = true
			if !strings.Contains(body, lokiauth.HashBearer(plaintext)) {
				t.Fatalf("hash secret missing token_hash: %s", body)
			}
			if strings.Contains(body, "token_encrypted") {
				t.Fatalf("hash secret contained ciphertext: %s", body)
			}
		}
		if strings.Contains(call.path, lokiQueryACLConfigMapName) || strings.Contains(body, lokiQueryACLConfigMapName) {
			sawACL = true
		}
		if strings.Contains(call.path, "ingresses") || strings.Contains(body, `"kind":"Ingress"`) {
			sawIngress = true
			if !strings.Contains(body, "loki-ingest.example.com") {
				t.Fatalf("ingress missing ingestHostname: %s", body)
			}
		}
	}
	if !sawSecret {
		t.Fatalf("did not apply hash secret, calls=%v", fake.calls)
	}
	if !sawACL {
		t.Fatalf("did not apply query ACL, calls=%v", fake.calls)
	}
	if !sawIngress {
		t.Fatalf("did not apply ingest Ingress, calls=%v", fake.calls)
	}
	meta := sharedStackMetadata(q.backend, "sharedLoki")
	if !boolFromAny(meta["ingestPublic"]) {
		raw, _ := json.Marshal(meta)
		t.Fatalf("ingestPublic not set after tokens+Ingress: %s", raw)
	}
}

func TestSecretInventoryDocumentsLokiIngestTokens(t *testing.T) {
	raw, err := os.ReadFile("../db/migrations/migration_secret_columns_test.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "003_loki_ingest_tokens.up.sql:token_hash") || !strings.Contains(s, "003_loki_ingest_tokens.up.sql:token_encrypted") {
		t.Fatal("migration_secret_columns_test.go must classify loki_ingest_tokens hash and Fernet columns")
	}
	inv, err := os.ReadFile("../../docs/secret-column-inventory.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(inv)
	if !strings.Contains(doc, "loki_ingest_tokens.token_hash") || !strings.Contains(doc, "loki_ingest_tokens.token_encrypted") {
		t.Fatal("docs/secret-column-inventory.md must list loki_ingest_tokens columns")
	}
}
