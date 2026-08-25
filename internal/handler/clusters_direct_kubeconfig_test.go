package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type directRequesterProbe struct{ calls int }

func (p *directRequesterProbe) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	p.calls++
	return nil, nil
}

func TestValidateDirectAccessConfigFailsClosed(t *testing.T) {
	t.Parallel()
	if err := validateDirectAccessConfig("", ""); err != nil {
		t.Fatalf("explicit empty endpoint and CA must clear direct access: %v", err)
	}
	for name, endpoint := range map[string]string{
		"malformed":     "://not-a-url",
		"plaintext":     "http://api.example.test:6443",
		"credentials":   "https://user:password@api.example.test:6443",
		"path":          "https://api.example.test:6443/api",
		"query":         "https://api.example.test:6443?token=secret",
		"wrong port":    "https://api.example.test:8443",
		"loopback":      "https://127.0.0.1:6443",
		"link local":    "https://169.254.169.254:443",
		"cluster DNS":   "https://kubernetes.default.svc:443",
		"local cluster": "https://kubernetes.default:443",
	} {
		name, endpoint := name, endpoint
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateDirectAccessConfig(endpoint, ""); err == nil {
				t.Fatalf("validateDirectAccessConfig(%q) unexpectedly succeeded", endpoint)
			}
		})
	}
	if err := validateDirectAccessConfig("https://api.example.test:6443", "not a certificate"); err == nil {
		t.Fatal("invalid CA unexpectedly accepted")
	}
	if err := validateDirectAccessConfig("", "not a certificate"); err == nil {
		t.Fatal("CA without endpoint unexpectedly accepted")
	}
}

func TestBuildDirectKubeconfigContainsOnlyScopedCredential(t *testing.T) {
	t.Parallel()
	ca := "-----BEGIN CERTIFICATE-----\nPUBLIC-CA-SENTINEL\n-----END CERTIFICATE-----"
	config := buildDirectKubeconfig(sqlc.Cluster{
		Name: "prod", ApiServerUrl: "https://api.example.test:6443/", CaCertificate: ca,
	}, "DIRECT-READER-TOKEN-SENTINEL")
	raw := string(mustJSON(t, config))
	for _, want := range []string{
		"https://api.example.test:6443", directReaderServiceAccount,
		"DIRECT-READER-TOKEN-SENTINEL", base64.StdEncoding.EncodeToString([]byte(ca)),
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("kubeconfig missing %q: %s", want, raw)
		}
	}
	for _, forbidden := range []string{"astronomer-agent-registration-token", "astronomer-agent-identity", "cluster-admin", "insecure-skip-tls-verify"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("kubeconfig exposed forbidden marker %q", forbidden)
		}
	}
}

func TestDirectKubeconfigAuditFailureMakesZeroRemoteCalls(t *testing.T) {
	id := uuid.New()
	q := newFakeAutoAttachClusterQuerier()
	q.clusters[id] = sqlc.Cluster{
		ID: id, Name: "prod", ApiServerUrl: "https://api.example.test:6443", IsLocal: false,
	}
	probe := &directRequesterProbe{}
	h := NewClusterHandler(q)
	h.SetDirectKubeconfigRequester(probe)
	h.SetRunTx(func(context.Context, func(ClusterMutationTx) error) error {
		return audit.ErrOutboxUnavailable
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+id.String()+"/generate-direct-kubeconfig/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.GenerateDirectKubeconfig(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if probe.calls != 0 {
		t.Fatalf("remote calls = %d, want zero before durable audit intent", probe.calls)
	}
}
