package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeSupportBundleQuerier struct {
	user      sqlc.User
	auditRows []sqlc.AuditLog
	audits    []sqlc.CreateAuditLogV1Params
}

func (f *fakeSupportBundleQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return []sqlc.Cluster{{
		ID:                uuid.New(),
		Name:              "prod-east",
		DisplayName:       "prod-east",
		Status:            "active",
		ApiServerUrl:      "https://api.example.test",
		CaCertificate:     "plain-ca-certificate",
		Environment:       "prod",
		Region:            "us-east",
		Provider:          "other",
		Distribution:      "k3s",
		AgentVersion:      "1.2.3",
		KubernetesVersion: "v1.30.0",
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}}, nil
}

func (f *fakeSupportBundleQuerier) ListUsers(context.Context, sqlc.ListUsersParams) ([]sqlc.User, error) {
	return []sqlc.User{{
		ID:          f.user.ID,
		Email:       "admin@example.com",
		Username:    "admin@example.com",
		Password:    "bcrypt-hash-should-not-leak",
		IsActive:    true,
		IsSuperuser: true,
		DateJoined:  time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
	}}, nil
}

func (f *fakeSupportBundleQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}

func (f *fakeSupportBundleQuerier) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{
		ID:           1,
		ServerUrl:    "https://operator:secret-pass@example.test",
		PlatformName: "Astronomer",
		InstanceID:   uuid.New(),
	}, nil
}

func (f *fakeSupportBundleQuerier) ListArgoCDInstances(context.Context, sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error) {
	lastSync := time.Now().UTC()
	return []sqlc.ArgocdInstance{{
		ID:                 uuid.New(),
		Name:               "builtin",
		ClusterID:          uuid.New(),
		ApiUrl:             "https://argocd.example.test",
		AuthTokenEncrypted: "encrypted-argocd-token-material",
		VerifySsl:          true,
		IsHealthy:          true,
		LastSync:           pgtype.Timestamptz{Time: lastSync, Valid: true},
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	}}, nil
}

func (f *fakeSupportBundleQuerier) ListAuditLogV1(context.Context, sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	if len(f.auditRows) > 0 {
		return f.auditRows, nil
	}
	detail := json.RawMessage(`{"password":"plaintext-password","authorization":"Bearer abc123","url":"https://user:secret@example.test/path"}`)
	return []sqlc.AuditLog{{
		ID:            uuid.New(),
		CreatedAt:     time.Now().UTC(),
		Action:        "cluster.delete",
		ResourceType:  "cluster",
		ResourceName:  "prod-east",
		Detail:        detail,
		CorrelationID: "corr-support-test",
	}}, nil
}

func (f *fakeSupportBundleQuerier) ListActiveConnections(context.Context) ([]sqlc.AgentConnection, error) {
	now := time.Now().UTC()
	return []sqlc.AgentConnection{{
		ID:           uuid.New(),
		ClusterID:    uuid.New(),
		AgentID:      "agent token=agent-secret",
		AgentVersion: "1.2.3",
		Status:       "connected",
		ConnectedAt:  now,
		LastPing:     pgtype.Timestamptz{Time: now, Valid: true},
	}}, nil
}

func (f *fakeSupportBundleQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.audits = append(f.audits, arg)
	return nil
}

func TestSupportBundleDownloadRedactsSensitiveValues(t *testing.T) {
	userID := uuid.New()
	q := &fakeSupportBundleQuerier{
		user: sqlc.User{ID: userID, Email: "admin@example.com", Username: "admin@example.com", IsActive: true, IsSuperuser: true},
	}
	h := NewSupportBundleHandler(q, nil, "")
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/support-bundle/", nil), userID)
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	files := readZipFiles(t, rec.Body.Bytes())
	combined := strings.Join(mapValues(files), "\n")
	for _, leaked := range []string{
		"plain-ca-certificate",
		"bcrypt-hash-should-not-leak",
		"encrypted-argocd-token-material",
		"plaintext-password",
		"Bearer abc123",
		"secret@example.test",
		"token=agent-secret",
	} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("support bundle leaked %q:\n%s", leaked, combined)
		}
	}
	for _, name := range []string{"meta.json", "clusters.json", "users.json", "argocd-instances.json", "audit-log-recent.json", "agent-connections.json", "README.txt"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("support bundle missing %s; files=%v", name, keys(files))
		}
	}
	if !strings.Contains(combined, "[redacted]") {
		t.Fatalf("support bundle did not contain redaction markers:\n%s", combined)
	}
	if len(q.audits) != 1 || q.audits[0].Action != "admin.support_bundle.downloaded" {
		t.Fatalf("support bundle audit rows = %#v", q.audits)
	}
}

func TestSupportBundleDownloadIncludesOperationalSummaries(t *testing.T) {
	userID := uuid.New()
	namespace := "astronomer"
	className := "nginx"
	tlsCert := testTLSCertPEM(t, "astronomer.dev.example.test")
	k8s := fake.NewClientset(
		&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "astronomer-default-deny", Namespace: namespace},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/part-of": "astronomer"}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			},
		},
		&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: "astronomer", Namespace: namespace},
			Spec: networkingv1.IngressSpec{
				IngressClassName: &className,
				Rules: []networkingv1.IngressRule{{
					Host: "astronomer.dev.example.test",
				}},
				TLS: []networkingv1.IngressTLS{{
					Hosts:      []string{"astronomer.dev.example.test"},
					SecretName: "astronomer-tls",
				}},
			},
			Status: networkingv1.IngressStatus{
				LoadBalancer: networkingv1.IngressLoadBalancerStatus{
					Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "192.0.2.10"}},
				},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "astronomer-tls", Namespace: namespace},
			Type:       corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       tlsCert,
				corev1.TLSPrivateKeyKey: []byte("private-key-should-not-leak"),
			},
		},
	)
	q := &fakeSupportBundleQuerier{
		user: sqlc.User{ID: userID, Email: "admin@example.com", Username: "admin@example.com", IsActive: true, IsSuperuser: true},
	}
	h := NewSupportBundleHandler(q, k8s, namespace)
	req := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/support-bundle/", nil), userID)
	rec := httptest.NewRecorder()

	h.Download(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	files := readZipFiles(t, rec.Body.Bytes())
	networkPolicies := string(files["networkpolicies.json"])
	if !strings.Contains(networkPolicies, "astronomer-default-deny") || !strings.Contains(networkPolicies, "Ingress") || !strings.Contains(networkPolicies, "Egress") {
		t.Fatalf("network policy summary missing expected content:\n%s", networkPolicies)
	}
	ingressCertificates := string(files["ingress-certificates.json"])
	for _, want := range []string{"astronomer.dev.example.test", "astronomer-tls", "not_after", "192.0.2.10"} {
		if !strings.Contains(ingressCertificates, want) {
			t.Fatalf("ingress/certificate summary missing %q:\n%s", want, ingressCertificates)
		}
	}
	if strings.Contains(ingressCertificates, "private-key-should-not-leak") || strings.Contains(ingressCertificates, "BEGIN CERTIFICATE") {
		t.Fatalf("ingress/certificate summary leaked secret material:\n%s", ingressCertificates)
	}
	argocdInstances := string(files["argocd-instances.json"])
	if !strings.Contains(argocdInstances, `"is_healthy": true`) || !strings.Contains(argocdInstances, `"last_sync":`) {
		t.Fatalf("argocd summary missing health fields:\n%s", argocdInstances)
	}
}

func TestWriteRedactedLogStreamRedactsSensitiveLines(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader("normal line\nAuthorization: Bearer abc123\npassword=secret\n")

	if err := writeRedactedLogStream(&out, input); err != nil {
		t.Fatalf("writeRedactedLogStream: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Bearer abc123") || strings.Contains(got, "password=secret") {
		t.Fatalf("log stream leaked sensitive line: %q", got)
	}
	if !strings.Contains(got, "normal line") || strings.Count(got, "[redacted sensitive log line]") != 2 {
		t.Fatalf("unexpected redacted log output: %q", got)
	}
}

func testTLSCertPEM(t *testing.T, host string) []byte {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func mapValues(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, string(v))
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
