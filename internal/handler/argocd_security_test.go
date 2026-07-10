package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
)

const argoHandlerCanary = "ARGO_HANDLER_CANARY_38b1ca"

type argoAuditBusCapture struct {
	names []string
	data  []any
}

func (c *argoAuditBusCapture) Publish(name string, data any) {
	c.names = append(c.names, name)
	c.data = append(c.data, data)
}

func TestArgoCDTypedCreateAndPatchRejectUnsafeSourcesBeforeUpstream(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		body   string
		call   func(*ArgoCDHandler, http.ResponseWriter, *http.Request)
		params map[string]string
	}{
		"create helm values": {
			method: http.MethodPost,
			body:   `{"name":"demo","spec":{"project":"default","source":{"repoURL":"https://git.example/repo","helm":{"values":"password: ` + argoHandlerCanary + `"}}}}`,
			call:   (*ArgoCDHandler).CreateApplication,
		},
		"patch values object": {
			method: http.MethodPatch,
			body:   `{"spec":{"source":{"helm":{"valuesObject":{"password":"` + argoHandlerCanary + `"}}}}}`,
			call:   (*ArgoCDHandler).PatchApplication,
			params: map[string]string{"name": "demo"},
		},
		"patch plugin env": {
			method: http.MethodPatch,
			body:   `{"spec":{"source":{"plugin":{"env":[{"name":"TOKEN","value":"` + argoHandlerCanary + `"}]}}}}`,
			call:   (*ArgoCDHandler).PatchApplication,
			params: map[string]string{"name": "demo"},
		},
		"patch unknown canary key": {
			method: http.MethodPatch,
			body:   `{"spec":{"source":{"` + argoHandlerCanary + `":"value"}}}`,
			call:   (*ArgoCDHandler).PatchApplication,
			params: map[string]string{"name": "demo"},
		},
		"applicationset template values": {
			method: http.MethodPost,
			body:   `{"name":"demo-set","spec":{"generators":[{"list":{"elements":[{"name":"demo"}]}}],"template":{"metadata":{"name":"{{name}}"},"spec":{"project":"default","source":{"repoURL":"https://git.example/repo","helm":{"values":"password: ` + argoHandlerCanary + `"}}}}}}`,
			call:   (*ArgoCDHandler).CreateApplicationSet,
		},
		"application destination signed URL": {
			method: http.MethodPost,
			body:   `{"name":"demo","spec":{"project":"default","source":{"repoURL":"https://git.example/repo"},"destination":{"server":"https://kube.example?X-Amz-Signature=` + argoHandlerCanary + `","namespace":"prod"}}}`,
			call:   (*ArgoCDHandler).CreateApplication,
		},
		"project sourceRepos signed URL": {
			method: http.MethodPost,
			body:   `{"name":"demo","spec":{"sourceRepos":["https://git.example/repo?Signature=` + argoHandlerCanary + `"]}}`,
			call:   (*ArgoCDHandler).CreateProject,
		},
		"repository signed URL": {
			method: http.MethodPost,
			body:   `{"repo":"https://git.example/repo?sig=` + argoHandlerCanary + `"}`,
			call:   (*ArgoCDHandler).CreateRepo,
		},
		"generator bearer scalar": {
			method: http.MethodPost,
			body:   `{"name":"demo-set","spec":{"generators":[{"list":{"elements":[{"note":"Bearer ` + argoHandlerCanary + `"}]}}],"template":{"metadata":{"name":"{{name}}"},"spec":{"project":"default","source":{"repoURL":"https://git.example/repo"},"destination":{"server":"{{server}}","namespace":"prod"}}}}}`,
			call:   (*ArgoCDHandler).CreateApplicationSet,
		},
	} {
		t.Run(name, func(t *testing.T) {
			upstreamCalls := 0
			h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"metadata":{"name":"demo"}}`))
			})
			params := map[string]string{"id": rec.instance.ID.String()}
			for key, value := range tc.params {
				params[key] = value
			}
			req := argoHandlerRouteRequest(tc.method, "/typed", tc.body, params)
			rr := httptest.NewRecorder()
			tc.call(h, rr, req)
			if rr.Code != http.StatusBadRequest || upstreamCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, upstreamCalls, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), argoHandlerCanary) {
				t.Fatalf("validation response leaked canary: %s", rr.Body.String())
			}
		})
	}
}

func TestArgoCDTypedResponseMalformedWrapperFailsClosed(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"status": map[string]any{"message": `{"token":"` + argoHandlerCanary}}}})
	})
	req := argoHandlerRouteRequest(http.MethodGet, "/typed", "", map[string]string{"id": rec.instance.ID.String()})
	rr := httptest.NewRecorder()
	h.LiveApplications(rr, req)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), argoHandlerCanary) || !strings.Contains(rr.Body.String(), "[redacted]") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateArgoCDInstanceRejectsCredentialURLBeforePersistence(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid instance URL reached upstream")
	})
	req := argoHandlerRouteRequest(http.MethodPost, "/typed", `{"name":"demo","cluster_id":"`+rec.instance.ClusterID.String()+`","api_url":"https://user:pass@example.test?sig=`+argoHandlerCanary+`"}`, nil)
	rr := httptest.NewRecorder()
	h.CreateInstance(rr, req)
	if rr.Code != http.StatusBadRequest || strings.Contains(rr.Body.String(), argoHandlerCanary) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestArgoCDTypedUpstreamResponsesAreSanitized(t *testing.T) {
	application := `{"metadata":{"name":"demo"},"spec":{"source":{"repoURL":"https://user:` + argoHandlerCanary + `@git.example/team/repo?token=` + argoHandlerCanary + `","helm":{"values":"password: ` + argoHandlerCanary + `","releaseName":"diagnostic-release"}}},"status":{"health":{"status":"Healthy"},"sync":{"status":"Synced"},"history":[{"source":{"helm":{"values":"` + argoHandlerCanary + `"}}}]}}`
	manifest := `{"manifests":["apiVersion: v1\nkind: Secret\nmetadata:\n  name: retained-name\nstringData:\n  password: ` + argoHandlerCanary + `\n"],"health":"Healthy"}`

	for name, tc := range map[string]struct {
		method string
		body   string
		params func(*argoCDQueryRecorder) map[string]string
		call   func(*ArgoCDHandler, http.ResponseWriter, *http.Request)
	}{
		"live list": {method: http.MethodGet, params: func(q *argoCDQueryRecorder) map[string]string { return map[string]string{"id": q.instance.ID.String()} }, call: (*ArgoCDHandler).LiveApplications},
		"history":   {method: http.MethodGet, params: func(q *argoCDQueryRecorder) map[string]string { return map[string]string{"id": q.app.ID.String()} }, call: (*ArgoCDHandler).AppHistory},
		"manifests": {method: http.MethodGet, params: func(q *argoCDQueryRecorder) map[string]string { return map[string]string{"id": q.app.ID.String()} }, call: (*ArgoCDHandler).AppManifests},
		"refresh":   {method: http.MethodPost, params: func(q *argoCDQueryRecorder) map[string]string { return map[string]string{"id": q.app.ID.String()} }, call: (*ArgoCDHandler).RefreshApp},
		"create":    {method: http.MethodPost, body: `{"name":"demo","spec":{"project":"default","source":{"repoURL":"https://git.example/repo","helm":{"releaseName":"safe"}}}}`, params: func(q *argoCDQueryRecorder) map[string]string { return map[string]string{"id": q.instance.ID.String()} }, call: (*ArgoCDHandler).CreateApplication},
		"patch": {method: http.MethodPatch, body: `{"spec":{"source":{"targetRevision":"main"}}}`, params: func(q *argoCDQueryRecorder) map[string]string {
			return map[string]string{"id": q.instance.ID.String(), "name": "demo"}
		}, call: (*ArgoCDHandler).PatchApplication},
	} {
		t.Run(name, func(t *testing.T) {
			h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "/manifests") {
					_, _ = w.Write([]byte(manifest))
					return
				}
				_, _ = w.Write([]byte(application))
			})
			req := argoHandlerRouteRequest(tc.method, "/typed", tc.body, tc.params(rec))
			rr := httptest.NewRecorder()
			tc.call(h, rr, req)
			if rr.Code < 200 || rr.Code >= 300 {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			text := rr.Body.String()
			if strings.Contains(text, argoHandlerCanary) || strings.Contains(text, "user:") {
				t.Fatalf("typed response leaked canary or userinfo: %s", text)
			}
			if !strings.Contains(text, "Healthy") {
				t.Fatalf("health diagnostic removed: %s", text)
			}
			if name != "manifests" {
				for _, diagnostic := range []string{"git.example", "/team/repo"} {
					if !strings.Contains(text, diagnostic) {
						t.Fatalf("URL diagnostic %q removed: %s", diagnostic, text)
					}
				}
			} else if !strings.Contains(text, "retained-name") {
				t.Fatalf("Secret identity diagnostic removed: %s", text)
			}
		})
	}
}

func TestArgoCDApplicationSetSafeWorkflowReachesUpstream(t *testing.T) {
	calls := 0
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"name":"demo-set"},"spec":{}}`))
	})
	body := `{"name":"demo-set","spec":{"generators":[{"list":{"elements":[{"name":"prod","server":"https://kube.example"}]}}],"template":{"metadata":{"name":"{{name}}-app","annotations":{"argocd.argoproj.io/sync-wave":"-1","argocd.argoproj.io/compare-options":"IgnoreExtraneous","notifications.argoproj.io/subscribe.on-sync-failed.slack":"platform-alerts"}},"spec":{"project":"default","source":{"repoURL":"https://git.example/repo","path":"deploy"},"destination":{"server":"{{server}}","namespace":"prod"}}}}}`
	req := argoHandlerRouteRequest(http.MethodPost, "/typed", body, map[string]string{"id": rec.instance.ID.String()})
	rr := httptest.NewRecorder()
	h.CreateApplicationSet(rr, req)
	if rr.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body.String())
	}
}

func TestArgoCDOperationResponseSanitizesErrorAndEventDetail(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) {})
	rec.operation = sqlc.ArgocdOperation{
		ID: rec.app.ID, TargetType: "application", TargetKey: rec.app.ID.String(), Status: "failed",
		ErrorMessage: "token=" + argoHandlerCanary + " sync failed", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	rec.operationEvents = []sqlc.ArgocdOperationEvent{{
		ID: uuid.New(), OperationID: rec.operation.ID, Level: "error", Stage: "sync",
		Message: "diagnostic event", Detail: json.RawMessage(`{"source":{"helm":{"values":"` + argoHandlerCanary + `"}},"health":"Degraded"}`), CreatedAt: time.Now(),
	}}
	req := argoHandlerRouteRequest(http.MethodGet, "/typed", "", map[string]string{"id": rec.operation.ID.String()})
	rr := httptest.NewRecorder()
	h.GetOperation(rr, req)
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), argoHandlerCanary) {
		t.Fatalf("operation response status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, diagnostic := range []string{"sync failed", "diagnostic event", "Degraded"} {
		if !strings.Contains(rr.Body.String(), diagnostic) {
			t.Fatalf("operation diagnostic %q removed: %s", diagnostic, rr.Body.String())
		}
	}
}

func TestArgoCDOperationEventPersistenceSanitizesMessageAndDetail(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) {})
	h.recordArgoCDOperationEvent(context.Background(), uuid.New(), "error", "sync", "token="+argoHandlerCanary+" failed", map[string]any{
		"source": map[string]any{"helm": map[string]any{"values": argoHandlerCanary}},
		"health": "Degraded",
	})
	if len(rec.events) != 1 {
		t.Fatalf("events=%d", len(rec.events))
	}
	raw, _ := json.Marshal(rec.events[0])
	if strings.Contains(string(raw), argoHandlerCanary) {
		t.Fatalf("persisted event leaked canary: %s", raw)
	}
	if !strings.Contains(string(raw), "Degraded") || !strings.Contains(string(raw), "failed") {
		t.Fatalf("persisted event lost diagnostics: %s", raw)
	}
}

func TestArgoCDTypedErrorsNeverEchoUpstreamBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/typed", nil)
	rr := httptest.NewRecorder()
	err := &argocdclient.APIError{
		Kind:    argocdclient.ErrServer,
		Status:  http.StatusInternalServerError,
		Message: "upstream message " + argoHandlerCanary,
		Body:    `{"token":"` + argoHandlerCanary + `"}`,
	}
	if !translateClientError(rr, req, err) {
		t.Fatal("typed error was not translated")
	}
	if strings.Contains(rr.Body.String(), argoHandlerCanary) || !strings.Contains(rr.Body.String(), "upstream service failed") {
		t.Fatalf("unsafe or unstable error response: %s", rr.Body.String())
	}
}

func TestRecordArgoAuditSanitizesPersistedAndPublishedEventInput(t *testing.T) {
	bus := &argoAuditBusCapture{}
	audit.SetBusPublisher(bus)
	t.Cleanup(func() { audit.SetBusPublisher(nil) })
	writer := &serviceProxyTestAuditWriter{}
	req := httptest.NewRequest(http.MethodPost, "/argocd", nil)
	recordArgoAudit(req, writer, "argocd.test", "argocd_resource", "id", "https://user:pass@example.test/repo?sig="+argoHandlerCanary+"#x", map[string]any{
		"reason":    `{"token":"` + argoHandlerCanary,
		"oversized": `{"safe":"` + strings.Repeat("x", (1<<20)+1) + `"}`,
		"note":      "credential=" + argoHandlerCanary,
		"server":    "https://user:pass@example.test/api?sig=" + argoHandlerCanary + "#x",
	})
	if len(writer.rows) != 1 || len(bus.data) != 1 {
		t.Fatalf("audit rows=%d published=%d", len(writer.rows), len(bus.data))
	}
	raw, _ := json.Marshal(map[string]any{"row": writer.rows[0], "published": bus.data[0]})
	if strings.Contains(string(raw), argoHandlerCanary) || strings.Contains(string(raw), "user:pass") || strings.Contains(string(raw), "?sig=") || strings.Contains(string(raw), "#x") {
		t.Fatalf("persisted/published audit input leaked diagnostics: %s", raw)
	}
	if !strings.Contains(string(raw), "example.test") {
		t.Fatalf("audit lost safe host diagnostic: %s", raw)
	}
}

func TestSyncReasonIsSanitizedBeforeDurableAndPublishedSinks(t *testing.T) {
	bus := &argoAuditBusCapture{}
	audit.SetBusPublisher(bus)
	t.Cleanup(func() { audit.SetBusPublisher(nil) })
	h, rec, _ := newArgoCDFixture(t, func(http.ResponseWriter, *http.Request) {})
	reason := "deploy https://user:pass@example.test/object?X-Amz-Signature=" + argoHandlerCanary + "#fragment token=" + argoHandlerCanary
	body := `{"reason":` + string(mustJSON(t, reason)) + `,"sync_window_override":true}`
	req := argoHandlerRouteRequest(http.MethodPost, "/typed", body, map[string]string{"id": rec.app.ID.String()})
	rr := httptest.NewRecorder()
	h.SyncApp(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(rec.created) != 1 || len(rec.auditRows) != 1 || len(bus.data) != 1 {
		t.Fatalf("created=%d audit=%d published=%d", len(rec.created), len(rec.auditRows), len(bus.data))
	}
	all, _ := json.Marshal(map[string]any{"payload": rec.created[0].Payload, "audit": rec.auditRows[0], "published": bus.data[0]})
	text := string(all)
	for _, forbidden := range []string{argoHandlerCanary, "user:pass", "?X-Amz", "#fragment"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("durable/published sync reason leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "https://example.test/object") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("sanitized reason lost safe context: %s", text)
	}
}

func argoHandlerRouteRequest(method, path, body string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	ctx := chi.NewRouteContext()
	for key, value := range params {
		ctx.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}
