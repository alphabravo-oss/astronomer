package handler

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// newVerifySSLFixture wires an ArgoCD handler whose warnings are captured, over
// a recorder holding one stored instance. Authorization is deliberately left
// unwired: with no authenticated user in the request context bindingsForContext
// reports "unrestricted", so these tests exercise the write path itself.
func newVerifySSLFixture(t *testing.T, stored sqlc.ArgocdInstance) (*ArgoCDHandler, *argoCDQueryRecorder, *bytes.Buffer) {
	t.Helper()
	rec := &argoCDQueryRecorder{instance: stored}
	h := NewArgoCDHandler(rec)
	logs := &bytes.Buffer{}
	h.SetLogger(slog.New(slog.NewTextHandler(logs, nil)))
	return h, rec, logs
}

func putInstance(t *testing.T, h *ArgoCDHandler, id uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/argocd/instances/"+id.String()+"/", strings.NewReader(body))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	rr := httptest.NewRecorder()
	h.UpdateInstance(rr, req)
	return rr
}

// A create that omits verify_ssl must store true. Before VerifySsl became a
// pointer the omitted field decoded to false and every such instance talked to
// its api_url — carrying the ArgoCD admin bearer token — without verifying TLS.
func TestCreateInstanceOmittedVerifySSLStoresVerifyOn(t *testing.T) {
	h, rec, logs := newVerifySSLFixture(t, sqlc.ArgocdInstance{})

	body := `{"name":"argocd","cluster_id":"` + uuid.New().String() + `","api_url":"https://argocd.example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/argocd/instances/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.CreateInstance(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.instanceCreates) != 1 {
		t.Fatalf("instanceCreates = %d, want 1", len(rec.instanceCreates))
	}
	if !rec.instanceCreates[0].VerifySsl {
		t.Fatal("omitted verify_ssl stored false; want true (fail-secure default)")
	}
	if strings.Contains(logs.String(), "verify_ssl=false") {
		t.Fatalf("unexpected skip-verify warning for a verifying instance: %s", logs.String())
	}
}

// An explicit false is still honored — self-signed ArgoCD endpoints remain
// reachable — but it is now loud rather than silent.
func TestCreateInstanceExplicitFalseVerifySSLIsHonoredAndWarned(t *testing.T) {
	h, rec, logs := newVerifySSLFixture(t, sqlc.ArgocdInstance{})

	body := `{"name":"argocd","cluster_id":"` + uuid.New().String() + `","api_url":"https://argocd.example.com","verify_ssl":false}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/argocd/instances/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.CreateInstance(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.instanceCreates) != 1 || rec.instanceCreates[0].VerifySsl {
		t.Fatalf("explicit verify_ssl=false was not honored: %+v", rec.instanceCreates)
	}
	if !strings.Contains(logs.String(), "verify_ssl=false") {
		t.Fatalf("no warning logged for a skip-verify instance: %s", logs.String())
	}
}

// A partial PUT (rename, change api_url) must not downgrade a verifying
// instance. Previously the omitted field decoded to false and the update was a
// full column replace, so any edit silently disabled verification.
func TestUpdateInstancePartialPutPreservesVerifySSL(t *testing.T) {
	id := uuid.New()
	stored := sqlc.ArgocdInstance{ID: id, ClusterID: uuid.New(), Name: "argocd", ApiUrl: "https://argocd.example.com", VerifySsl: true}
	h, rec, logs := newVerifySSLFixture(t, stored)

	rr := putInstance(t, h, id, `{"name":"argocd-renamed","api_url":"https://argocd.example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.instanceUpdates) != 1 {
		t.Fatalf("instanceUpdates = %d, want 1", len(rec.instanceUpdates))
	}
	if !rec.instanceUpdates[0].VerifySsl {
		t.Fatal("partial PUT downgraded verify_ssl to false; want the stored true preserved")
	}
	if strings.Contains(logs.String(), "verify_ssl=false") {
		t.Fatalf("unexpected skip-verify warning: %s", logs.String())
	}
}

// The mirror case: an instance stored insecure keeps that operator choice when
// the field is omitted, rather than being silently "upgraded" behind an
// operator who would then see connection failures.
func TestUpdateInstancePartialPutPreservesStoredSkipVerify(t *testing.T) {
	id := uuid.New()
	stored := sqlc.ArgocdInstance{ID: id, ClusterID: uuid.New(), Name: "argocd", ApiUrl: "https://argocd.example.com", VerifySsl: false}
	h, rec, _ := newVerifySSLFixture(t, stored)

	rr := putInstance(t, h, id, `{"name":"argocd-renamed","api_url":"https://argocd.example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.instanceUpdates) != 1 || rec.instanceUpdates[0].VerifySsl {
		t.Fatalf("stored verify_ssl=false was not preserved: %+v", rec.instanceUpdates)
	}
}

// An explicit true on a previously-insecure instance must take effect.
func TestUpdateInstanceExplicitTrueUpgradesVerifySSL(t *testing.T) {
	id := uuid.New()
	stored := sqlc.ArgocdInstance{ID: id, ClusterID: uuid.New(), Name: "argocd", ApiUrl: "https://argocd.example.com", VerifySsl: false}
	h, rec, _ := newVerifySSLFixture(t, stored)

	rr := putInstance(t, h, id, `{"name":"argocd","api_url":"https://argocd.example.com","verify_ssl":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.instanceUpdates) != 1 || !rec.instanceUpdates[0].VerifySsl {
		t.Fatalf("explicit verify_ssl=true was not honored: %+v", rec.instanceUpdates)
	}
}

// instanceHTTPClient is what argoCDClient injects as Options.HTTPClient, and an
// injected client wins over Options.SkipTLSVerify inside NewClient — so this is
// the client that actually decides TLS verification. A verifying row must never
// yield a transport with InsecureSkipVerify set.
func TestInstanceHTTPClientVerifiesForVerifyingInstance(t *testing.T) {
	h := NewArgoCDHandler(&argoCDQueryRecorder{})

	verifying := h.instanceHTTPClient(sqlc.ArgocdInstance{VerifySsl: true})
	if verifying == nil {
		t.Fatal("instanceHTTPClient returned nil; callers invoke .Do on it directly")
	}
	if skipsTLSVerify(t, verifying) {
		t.Fatal("verify_ssl=true produced an InsecureSkipVerify client")
	}
	if !skipsTLSVerify(t, h.instanceHTTPClient(sqlc.ArgocdInstance{VerifySsl: false})) {
		t.Fatal("verify_ssl=false did not produce a skip-verify client")
	}
}

func skipsTLSVerify(t *testing.T, c *http.Client) bool {
	t.Helper()
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", c.Transport)
	}
	return transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify
}
