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

func persistHealthyLoki(t *testing.T, h *MonitoringHandler, q *stackLifecycleQuerier) {
	t.Helper()
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
}

func TestReconcileLokiIngestWritesHashSecretWithoutPlaintext(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	fake := &loggingFakeRequester{}
	h.requester = fake
	persistHealthyLoki(t, h, q)
	plaintext := "never-project-this"
	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: lokiauth.HashBearer(plaintext),
	}}

	if err := h.ReconcileLokiIngest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var sawSecret, sawIngress, sawCert, sawACL bool
	for _, call := range fake.calls {
		body := string(call.body)
		if strings.Contains(body, plaintext) {
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
			if !strings.Contains(body, "cert-manager.io/issuer") && !strings.Contains(body, "cert-manager.io/cluster-issuer") {
				t.Fatalf("ingress missing cert-manager annotation: %s", body)
			}
		}
		if strings.Contains(call.path, "certificates") || strings.Contains(body, `"kind":"Certificate"`) {
			sawCert = true
			if !strings.Contains(body, "loki-ingest.example.com") || !strings.Contains(body, lokiIngestTLSSecretName) {
				t.Fatalf("certificate missing host or secret: %s", body)
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
	if !sawCert {
		t.Fatalf("did not apply cert-manager Certificate, calls=%v", fake.calls)
	}
	meta := sharedStackMetadata(q.backend, "sharedLoki")
	if !boolFromAny(meta["ingestPublic"]) {
		raw, _ := json.Marshal(meta)
		t.Fatalf("ingestPublic not set after tokens+Ingress: %s", raw)
	}
}

func TestReconcileLokiIngestGatewayEmitsHTTPRouteAndCertificate(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	fake := &loggingFakeRequester{}
	h.requester = fake
	h.SetGrafanaExpose(GrafanaExpose{
		GatewayClass:      "envoy",
		GatewayName:       "astronomer",
		PlatformNamespace: "astronomer",
		TLSIssuerName:     "astronomer-tls",
		TLSIssuerKind:     "Issuer",
	})
	persistHealthyLoki(t, h, q)
	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: "abc",
	}}
	if err := h.ReconcileLokiIngest(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	var sawRoute, sawCert, sawIngress, sawGatewayListener bool
	for _, call := range fake.calls {
		body := string(call.body)
		if strings.Contains(body, `"kind":"HTTPRoute"`) && strings.Contains(body, "/loki/api/v1/push") {
			sawRoute = true
			if !strings.Contains(body, lokiIngestGatewayListener) {
				t.Fatalf("HTTPRoute must attach to ingest listener: %s", body)
			}
		}
		if strings.Contains(body, `"kind":"Certificate"`) && strings.Contains(body, "loki-ingest.example.com") {
			sawCert = true
			if !strings.Contains(call.path, "/namespaces/astronomer/certificates/") && !strings.Contains(body, `"namespace":"astronomer"`) {
				t.Fatalf("Certificate must live next to the Gateway/Issuer: %s %s", call.path, body)
			}
		}
		if strings.Contains(body, `"kind":"Ingress"`) {
			sawIngress = true
		}
		if (call.method == "PUT" || call.method == "PATCH") && strings.Contains(call.path, "/gateways/astronomer") {
			if strings.Contains(body, lokiIngestGatewayListener) &&
				strings.Contains(body, lokiIngestTLSSecretName) &&
				strings.Contains(body, "loki-ingest.example.com") {
				sawGatewayListener = true
			}
		}
	}
	if !sawRoute || !sawCert {
		t.Fatalf("gateway expose missing HTTPRoute/Certificate, calls=%v", fake.calls)
	}
	if !sawGatewayListener {
		t.Fatalf("Gateway listener must terminate ingestHostname with astronomer-loki-ingest-tls, calls=%v", fake.calls)
	}
	if sawIngress {
		t.Fatal("gateway expose must not also apply Ingress")
	}
}

func TestReconcileLokiIngestUninstallDeletesPublicPath(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	fake := &loggingFakeRequester{}
	h.requester = fake
	persistHealthyLoki(t, h, q)
	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: "abc",
	}}
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		StorageConfigID:     stackTestStorageID,
		IngestHostname:      "loki-ingest.example.com",
	}, "uninstalled"); err != nil {
		t.Fatalf("uninstall persist: %v", err)
	}
	if err := h.ReconcileLokiIngest(context.Background()); err != nil {
		t.Fatalf("reconcile after uninstall: %v", err)
	}
	var sawDeleteIngress, sawDeleteCert bool
	for _, call := range fake.calls {
		if call.method != "DELETE" {
			continue
		}
		if strings.Contains(call.path, "ingresses/astronomer-loki-auth") {
			sawDeleteIngress = true
		}
		if strings.Contains(call.path, "certificates/"+lokiIngestTLSSecretName) {
			sawDeleteCert = true
		}
	}
	if !sawDeleteIngress || !sawDeleteCert {
		t.Fatalf("uninstall did not delete public ingest objects, calls=%v", fake.calls)
	}
	meta := sharedStackMetadata(q.backend, "sharedLoki")
	if boolFromAny(meta["ingestPublic"]) {
		t.Fatalf("ingestPublic stayed true after uninstall: %v", meta["ingestPublic"])
	}
}

func TestBuildLokiQueryACLGrantsAndDedupes(t *testing.T) {
	c1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	adminRules := json.RawMessage(`[{"verbs":["create","read","update","delete","list"],"resource":"monitoring"}]`)
	viewerRules := json.RawMessage(`[{"verbs":["read","list"],"resource":"monitoring"}]`)
	workloadRules := json.RawMessage(`[{"verbs":["create","read","update","delete","list"],"resource":"workloads"}]`)
	acl := buildLokiQueryACLFromCandidates(
		[]sqlc.ListLokiQueryACLAdminCandidatesRow{
			{Email: "admin@example.com", IsSuperuser: false, Rules: adminRules},
			{Email: "root@example.com", IsSuperuser: true, Rules: json.RawMessage(`[]`)},
			{Email: "fleet-viewer@example.com", IsSuperuser: false, Rules: viewerRules},
		},
		[]sqlc.ListLokiQueryACLUserCandidatesRow{
			{Email: "viewer@example.com", ClusterID: c1, Rules: viewerRules},
			{Email: "viewer@example.com", ClusterID: c1, Rules: viewerRules},
			{Email: "editor@example.com", ClusterID: c1, Rules: workloadRules},
			{Email: "admin@example.com", ClusterID: c1, Rules: viewerRules},
			{Email: "fleet-viewer@example.com", ClusterID: c1, Rules: viewerRules},
		},
	)
	if len(acl.Admins) != 2 {
		t.Fatalf("admins = %v, want monitoring-admin + superuser", acl.Admins)
	}
	for _, email := range acl.Admins {
		if email == "fleet-viewer@example.com" {
			t.Fatal("global monitoring:read must not be a fleet Loki admin")
		}
	}
	if _, ok := acl.Users["admin@example.com"]; ok {
		t.Fatal("admins must not also appear in users")
	}
	if got := acl.Users["viewer@example.com"]; len(got) != 1 || got[0] != c1.String() {
		t.Fatalf("viewer allow-list = %v, want sole cluster (deduped)", got)
	}
	org, status := lokiauth.SelectOrg("", acl.Users["viewer@example.com"])
	if status != 0 || org != c1.String() {
		t.Fatalf("deduped viewer sole-tenant status=%d org=%q", status, org)
	}
	if _, ok := acl.Users["editor@example.com"]; ok {
		t.Fatal("workload-editor must not receive a Loki org")
	}
	if got := acl.Users["fleet-viewer@example.com"]; len(got) != 1 || got[0] != c1.String() {
		t.Fatalf("global monitoring:read must only get matching cluster orgs, got %v", got)
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
