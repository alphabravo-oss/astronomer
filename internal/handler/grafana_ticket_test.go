package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

func grafanaTicketHandler(t *testing.T, bindings []rbac.RoleBinding) *MonitoringHandler {
	t.Helper()
	h, _ := newStackLifecycleHandler(t)
	h.SetServerURL("https://astronomer.example.com")
	h.SetGrafanaTickets(auth.NewGrafanaTicketStore(time.Minute))
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: bindings})
	return h
}

func issueGrafanaTicket(t *testing.T, h *MonitoringHandler, email, role string, explore, admin bool, ttl time.Duration, clusterIDs []string) string {
	t.Helper()
	token, _, err := h.grafanaTickets.Issue(uuid.New(), email, role, explore, admin, ttl, clusterIDs)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func redeemGrafanaTicket(h *MonitoringHandler, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/observability/grafana-ticket/redeem/", strings.NewReader(`{"ticket":"`+token+`"}`))
	h.RedeemGrafanaTicket(rec, req)
	return rec
}

func TestGrafanaTicketRedeemOneUse(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	ticket := issueGrafanaTicket(t, h, "ops@example.com", "Editor", true, false, 15*time.Minute, nil)

	first := redeemGrafanaTicket(h, ticket)
	if first.Code != http.StatusOK {
		t.Fatalf("redeem status = %d: %s", first.Code, first.Body.String())
	}
	var wrap struct {
		Data struct {
			Email string `json:"email"`
			Role  string `json:"role"`
			TTL   int    `json:"ttl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	if wrap.Data.Email != "ops@example.com" || wrap.Data.Role != "Editor" || wrap.Data.TTL != 15*60 {
		t.Fatalf("redeem payload = %+v", wrap.Data)
	}

	second := redeemGrafanaTicket(h, ticket)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want 401: %s", second.Code, second.Body.String())
	}
}

func TestGrafanaTicketRedeemMissing(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	rec := httptest.NewRecorder()
	h.RedeemGrafanaTicket(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestGrafanaIdentityClusterScopedIncludesClusterIDsAndExplore(t *testing.T) {
	clusterID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	h := grafanaTicketHandler(t, []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceMonitoring),
			Verbs:    []string{string(rbac.VerbRead)},
		}},
	}})
	user := &reqctx.User{ID: uuid.NewString(), Email: "scoped@example.com", AuthMethod: "jwt"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), user))
	email, role, explore, admin, clusterIDs := h.grafanaIdentity(req, user)

	if email != "scoped@example.com" || role != "Viewer" || admin || !explore {
		t.Fatalf("identity = email=%q role=%q explore=%v admin=%v", email, role, explore, admin)
	}
	if len(clusterIDs) != 1 || clusterIDs[0] != clusterID.String() {
		t.Fatalf("clusterIds = %v", clusterIDs)
	}
}

func TestGrafanaIdentityGlobalReadDoesNotGetClusterRewriteList(t *testing.T) {
	h := grafanaTicketHandler(t, []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceMonitoring),
			Verbs:    []string{string(rbac.VerbRead)},
		}},
	}})
	user := &reqctx.User{ID: uuid.NewString(), Email: "reader@example.com", AuthMethod: "jwt"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/grafana/", nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), user))
	_, role, explore, admin, clusterIDs := h.grafanaIdentity(req, user)
	if role != "Viewer" || explore || admin || len(clusterIDs) != 0 {
		t.Fatalf("global monitoring:read must stay Explore-locked without rewrite list: role=%q explore=%v admin=%v clusterIDs=%v", role, explore, admin, clusterIDs)
	}
}

func TestGrafanaCookieTTLUsesSessionSetting(t *testing.T) {
	h := grafanaTicketHandler(t, grantMonitoring())
	h.SetSessionTTL(func(context.Context) time.Duration { return 15 * time.Minute })
	if got := h.grafanaCookieTTL(context.Background()); got != 15*time.Minute {
		t.Fatalf("ttl = %s, want 15m", got)
	}
}
