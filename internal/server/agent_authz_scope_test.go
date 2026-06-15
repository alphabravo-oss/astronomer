package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// routeSecurityMultiRuleBindings builds a single binding carrying every
// supplied (resource, verbs...) rule so a test can grant the precise
// RBAC needed by a typed mutation route.
func routeSecurityMultiRuleBindings(rules ...rbac.Rule) []rbac.RoleBinding {
	return []rbac.RoleBinding{{RoleRules: rules}}
}

func rule(resource rbac.Resource, verbs ...rbac.Verb) rbac.Rule {
	vs := make([]string, 0, len(verbs))
	for _, v := range verbs {
		vs = append(vs, string(v))
	}
	return rbac.Rule{Resource: string(resource), Verbs: vs}
}

// typedMutationCase pins one H1 typed mutation route, the RBAC verb it
// requires, and a sample request that should pass route validation so a
// scope+RBAC-authorized caller reaches the handler.
type typedMutationCase struct {
	name       string
	method     string
	path       string
	body       string
	rbacRule   rbac.Rule
	otherScope string // a non-clusters write scope insufficient for this route
}

func typedMutationCases(clusterID string) []typedMutationCase {
	return []typedMutationCase{
		{
			name:     "workload scale",
			method:   http.MethodPatch,
			path:     "/api/v1/clusters/" + clusterID + "/workloads/deployments/default/web/scale/",
			body:     `{"replicas":2}`,
			rbacRule: rule(rbac.ResourceWorkloads, rbac.VerbScale),
		},
		{
			name:     "workload delete",
			method:   http.MethodDelete,
			path:     "/api/v1/clusters/" + clusterID + "/workloads/deployments/default/web/",
			rbacRule: rule(rbac.ResourceWorkloads, rbac.VerbDelete),
		},
		{
			name:     "node drain",
			method:   http.MethodPost,
			path:     "/api/v1/nodes/" + clusterID + "/node-1/drain/",
			rbacRule: rule(rbac.ResourceNodes, rbac.VerbManage),
		},
		{
			name:     "resource create",
			method:   http.MethodPost,
			path:     "/api/v1/clusters/" + clusterID + "/resources/services/",
			body:     `{}`,
			rbacRule: rule(rbac.ResourceServices, rbac.VerbCreate),
		},
	}
}

func newTypedMutationRouter(rawToken string, userID uuid.UUID, scopes json.RawMessage, bindings []rbac.RoleBinding) http.Handler {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:         jwtMgr,
		AuthQueries: routeSecurityAPITokenQuerier(rawToken, userID, scopes),
		RBACEngine:  rbac.NewEngine(),
		RBACQueries: routeSecurityRBACQuerier{bindings: bindings},
		Resources:   handler.NewResourceHandler(),
		Workloads:   handler.NewWorkloadHandler(),
	})
}

// TestTypedMutationRoutesRejectReadScopedTokens is the H1 negative test:
// a read-scoped API token whose owner ALSO holds the matching RBAC verb
// is still denied on every typed mutation route, while a clusters:write
// token with the same RBAC reaches the handler, and a missing RBAC verb
// is denied regardless of scope.
func TestTypedMutationRoutesRejectReadScopedTokens(t *testing.T) {
	userID := uuid.New()
	clusterID := uuid.New().String()

	for _, tc := range typedMutationCases(clusterID) {
		t.Run(tc.name, func(t *testing.T) {
			fullRBAC := routeSecurityMultiRuleBindings(tc.rbacRule)

			// (1) read-scoped token + full RBAC => 403 scope_denied.
			readRouter := newTypedMutationRouter("astro_read_"+tc.name, userID, json.RawMessage(`["read"]`), fullRBAC)
			readRec := doRequest(readRouter, tc.method, tc.path, "astro_read_"+tc.name, tc.body)
			if readRec.Code != http.StatusForbidden {
				t.Fatalf("read-scoped token status = %d, want %d; body=%s", readRec.Code, http.StatusForbidden, readRec.Body.String())
			}
			if !strings.Contains(readRec.Body.String(), "scope_denied") {
				t.Fatalf("read-scoped token body = %s, want scope_denied", readRec.Body.String())
			}

			// (3) clusters:write token + full RBAC => reaches handler (not 403).
			writeRouter := newTypedMutationRouter("astro_write_"+tc.name, userID, json.RawMessage(`["clusters:write"]`), fullRBAC)
			writeRec := doRequest(writeRouter, tc.method, tc.path, "astro_write_"+tc.name, tc.body)
			if writeRec.Code == http.StatusForbidden {
				t.Fatalf("write-scoped token with RBAC status = %d (forbidden); body=%s", writeRec.Code, writeRec.Body.String())
			}

			// (4) clusters:write token but NO matching RBAC => 403 regardless of scope.
			noRBACRouter := newTypedMutationRouter("astro_norbac_"+tc.name, userID, json.RawMessage(`["clusters:write"]`), routeSecurityReadOnlyBindings())
			noRBACRec := doRequest(noRBACRouter, tc.method, tc.path, "astro_norbac_"+tc.name, tc.body)
			if noRBACRec.Code != http.StatusForbidden {
				t.Fatalf("write-scoped token without RBAC status = %d, want %d; body=%s", noRBACRec.Code, http.StatusForbidden, noRBACRec.Body.String())
			}
		})
	}
}

func doRequest(h http.Handler, method, path, rawToken, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// streamTicketRouter wires the StreamTickets handler + API-token auth so a
// raw-bearer caller can attempt to mint an exec/shell/logs ticket.
func newStreamTicketRouter(rawToken string, userID uuid.UUID, scopes json.RawMessage, bindings []rbac.RoleBinding) http.Handler {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	ticketStore := auth.NewStreamTicketStore(0)
	ticketHandler := handler.NewStreamTicketHandler(ticketStore)
	ticketHandler.SetAuthorization(rbac.NewEngine(), routeSecurityRBACQuerier{bindings: bindings})
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:               jwtMgr,
		AuthQueries:       routeSecurityAPITokenQuerier(rawToken, userID, scopes),
		RBACEngine:        rbac.NewEngine(),
		RBACQueries:       routeSecurityRBACQuerier{bindings: bindings},
		StreamTickets:     ticketHandler,
		StreamTicketStore: ticketStore,
	})
}

func issueTicket(h http.Handler, rawToken, streamType, clusterID string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"stream_type": streamType, "cluster_id": clusterID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/tickets/", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestExecShellTicketIssuanceRejectsReadScopedTokens is half of the H2
// negative test: a read-scoped token (even with clusters:update RBAC)
// cannot mint an exec or shell ticket, but a clusters:write token with
// the same RBAC can. Logs tickets stay read-eligible. Missing RBAC is
// denied regardless of scope.
func TestExecShellTicketIssuanceRejectsReadScopedTokens(t *testing.T) {
	userID := uuid.New()
	clusterID := uuid.New().String()
	updateRBAC := routeSecurityMultiRuleBindings(rule(rbac.ResourceClusters, rbac.VerbUpdate, rbac.VerbRead))

	for _, kind := range []string{auth.StreamKindExec, auth.StreamKindShell} {
		t.Run("read_token_"+kind, func(t *testing.T) {
			router := newStreamTicketRouter("astro_ticket_read_"+kind, userID, json.RawMessage(`["read"]`), updateRBAC)
			rec := issueTicket(router, "astro_ticket_read_"+kind, kind, clusterID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("read-scoped %s ticket status = %d, want %d; body=%s", kind, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "scope_denied") {
				t.Fatalf("read-scoped %s ticket body = %s, want scope_denied", kind, rec.Body.String())
			}
		})

		t.Run("write_token_"+kind, func(t *testing.T) {
			router := newStreamTicketRouter("astro_ticket_write_"+kind, userID, json.RawMessage(`["clusters:write"]`), updateRBAC)
			rec := issueTicket(router, "astro_ticket_write_"+kind, kind, clusterID)
			if rec.Code != http.StatusCreated {
				t.Fatalf("write-scoped %s ticket status = %d, want %d; body=%s", kind, rec.Code, http.StatusCreated, rec.Body.String())
			}
		})

		t.Run("no_rbac_"+kind, func(t *testing.T) {
			router := newStreamTicketRouter("astro_ticket_norbac_"+kind, userID, json.RawMessage(`["clusters:write"]`), routeSecurityReadOnlyBindings())
			rec := issueTicket(router, "astro_ticket_norbac_"+kind, kind, clusterID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("write-scoped %s ticket without RBAC status = %d, want %d; body=%s", kind, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}

	// Logs tickets remain read-eligible: a read-scoped token with cluster
	// read RBAC can still mint one.
	t.Run("read_token_logs_allowed", func(t *testing.T) {
		readRBAC := routeSecurityMultiRuleBindings(rule(rbac.ResourceClusters, rbac.VerbRead))
		router := newStreamTicketRouter("astro_ticket_logs", userID, json.RawMessage(`["read"]`), readRBAC)
		rec := issueTicket(router, "astro_ticket_logs", auth.StreamKindLogs, clusterID)
		if rec.Code != http.StatusCreated {
			t.Fatalf("read-scoped logs ticket status = %d, want %d; body=%s", rec.Code, http.StatusCreated, rec.Body.String())
		}
	})
}

// rawBearerExecRouter wires the exec/logs WS consumers with API-token
// stream auth so a raw `Authorization: Bearer astro_*` handshake exercises
// the AuthorizeStreamRequestWithTickets scope backstop.
func newRawBearerStreamRouter(rawToken string, userID uuid.UUID, scopes json.RawMessage) http.Handler {
	jwtMgr := auth.NewJWTManager("route-security-test-secret", 60)
	authQueries := routeSecurityAPITokenQuerier(rawToken, userID, scopes)
	hub := tunnel.NewHub(slog.Default())
	execConsumer := tunnel.NewExecConsumer(hub, slog.Default())
	execConsumer.SetAuth(jwtMgr, authQueries)
	logsConsumer := tunnel.NewLogsConsumer(hub, slog.Default())
	logsConsumer.SetAuth(jwtMgr, authQueries)
	return NewRouter(&config.Config{}, RouterDependencies{
		JWT:  jwtMgr,
		Exec: execConsumer,
		Logs: logsConsumer,
	})
}

// TestRawBearerExecRejectsReadScopedTokens is the other half of the H2
// negative test: a read-scoped API token presented as a raw bearer on the
// /ws/exec handshake is rejected (401) by the stream auth scope backstop,
// even though it is a valid, active token. Logs stay read-eligible.
func TestRawBearerExecRejectsReadScopedTokens(t *testing.T) {
	userID := uuid.New()
	clusterID := uuid.New().String()

	execPath := "/api/v1/ws/exec/" + clusterID + "/default/example/shell/"
	logsPath := "/api/v1/ws/logs/" + clusterID + "/default/example/app/"

	// (2) read-scoped token => exec handshake rejected with 401.
	readRouter := newRawBearerStreamRouter("astro_exec_read", userID, json.RawMessage(`["read"]`))
	readReq := httptest.NewRequest(http.MethodGet, execPath, nil)
	readReq.Header.Set("Authorization", "Bearer astro_exec_read")
	readRec := httptest.NewRecorder()
	readRouter.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusUnauthorized {
		t.Fatalf("read-scoped raw-bearer exec status = %d, want %d; body=%s", readRec.Code, http.StatusUnauthorized, readRec.Body.String())
	}

	// Logs stay read-eligible: a read-scoped token must NOT be rejected by
	// the scope gate (it fails later at the WS upgrade / agent dial, which
	// surfaces as a non-401 status from this in-process handler).
	logsReadRouter := newRawBearerStreamRouter("astro_logs_read", userID, json.RawMessage(`["read"]`))
	logsReq := httptest.NewRequest(http.MethodGet, logsPath, nil)
	logsReq.Header.Set("Authorization", "Bearer astro_logs_read")
	logsRec := httptest.NewRecorder()
	logsReadRouter.ServeHTTP(logsRec, logsReq)
	if logsRec.Code == http.StatusUnauthorized {
		t.Fatalf("read-scoped raw-bearer logs status = %d (rejected); logs must stay read-eligible; body=%s", logsRec.Code, logsRec.Body.String())
	}
}
