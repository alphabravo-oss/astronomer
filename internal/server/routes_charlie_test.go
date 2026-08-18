package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestCharlieOperationRouteUsesCanonicalTrailingSlash(t *testing.T) {
	router := NewRouter(&config.Config{}, RouterDependencies{
		CharlieOperations: handler.NewCharlieOperationHandler(nil),
	})

	found := false
	if err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet && route == "/api/v1/charlie/operations/{operation_id}/" {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatal("Charlie operation route must end in / because API normalization appends it before chi matches")
	}
}

type charlieEnabledSettings struct{}

func (charlieEnabledSettings) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{Key: key, Value: json.RawMessage("true")}, nil
}

func TestAPIRequestTimeoutClassesCharlieReconciliationAndEventStreams(t *testing.T) {
	handler := apiRequestTimeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, hasDeadline := r.Context().Deadline()
		remaining := time.Duration(0)
		if hasDeadline {
			remaining = time.Until(deadline)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"deadline": hasDeadline, "remaining_ms": remaining.Milliseconds()})
	}))
	for _, test := range []struct {
		method, path string
		deadline     bool
		long         bool
	}{
		{http.MethodGet, "/api/v1/charlie/sessions/session-a/events/", false, false},
		{http.MethodGet, "/api/v1/charlie/sessions/session-a/history/", true, false},
		{http.MethodPost, "/api/v1/charlie/sessions/session-a/events/", true, false},
		{http.MethodGet, "/api/v1/clusters/", true, false},
		{http.MethodPost, "/api/v1/admin/charlie/onboarding/consume/", true, true},
		{http.MethodPost, "/api/v1/admin/charlie/disconnect/", true, true},
		{http.MethodPatch, "/api/v1/admin/charlie/mode/", true, true},
		{http.MethodPut, "/api/v1/admin/charlie/kubernetes-visibility/", true, true},
		{http.MethodGet, "/api/v1/admin/charlie/kubernetes-visibility/", true, false},
		{http.MethodPost, "/api/v1/admin/charlie/diagnostics/run/", true, false},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		var response struct {
			Deadline    bool  `json:"deadline"`
			RemainingMS int64 `json:"remaining_ms"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Deadline != test.deadline {
			t.Fatalf("%s %s deadline=%v, want %v", test.method, test.path, response.Deadline, test.deadline)
		}
		if test.long && response.RemainingMS < (charlieAdminReconciliationTimeout-time.Second).Milliseconds() {
			t.Fatalf("%s %s remaining=%dms, want long reconciliation deadline", test.method, test.path, response.RemainingMS)
		}
		if !test.long && test.deadline && response.RemainingMS > (2*time.Second).Milliseconds() {
			t.Fatalf("%s %s remaining=%dms, want ordinary REST deadline", test.method, test.path, response.RemainingMS)
		}
	}
}

func TestCharlieAdminRoutesRejectNonAdminWithoutManagePermission(t *testing.T) {
	jwt := auth.MustNewJWTManager("charlie-route-security-test-secret", 60)
	token, err := jwt.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	router := NewRouter(&config.Config{}, RouterDependencies{
		JWT:               jwt,
		RBACEngine:        rbac.NewEngine(),
		RBACQueries:       routeSecurityRBACQuerier{bindings: routeSecurityBindings(rbac.ResourceCharlie, rbac.VerbRead)},
		SettingsCache:     handler.NewSettingsCache(charlieEnabledSettings{}, time.Minute),
		CharlieAdmin:      handler.NewCharlieAdminHandler(nil, nil),
		CharlieOnboarding: handler.NewCharlieOnboardingHandler(nil),
	})

	eventID := "22222222-2222-4222-8222-222222222222"
	ruleID := "11111111-1111-4111-8111-111111111111"
	requests := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/admin/charlie/onboarding/validate/"},
		{http.MethodPost, "/api/v1/admin/charlie/onboarding/consume/"},
		{http.MethodGet, "/api/v1/admin/charlie/status/"},
		{http.MethodPost, "/api/v1/admin/charlie/disconnect/"},
		{http.MethodPatch, "/api/v1/admin/charlie/mode/"},
		{http.MethodGet, "/api/v1/admin/charlie/kubernetes-visibility/"},
		{http.MethodPut, "/api/v1/admin/charlie/kubernetes-visibility/"},
		{http.MethodGet, "/api/v1/admin/charlie/trigger-rules/"},
		{http.MethodGet, "/api/v1/admin/charlie/alert-deliveries/?finding_id=11111111-1111-4111-8111-111111111111"},
		{http.MethodPost, "/api/v1/admin/charlie/qualification/discovery/"},
		{http.MethodPut, "/api/v1/admin/charlie/action-policies/astronomer.queue.retry_task/"},
		{http.MethodPost, "/api/v1/admin/charlie/trigger-rules/"},
		{http.MethodPatch, "/api/v1/admin/charlie/trigger-rules/" + ruleID + "/"},
		{http.MethodDelete, "/api/v1/admin/charlie/trigger-rules/" + ruleID + "/"},
		{http.MethodGet, "/api/v1/admin/charlie/trigger-events/"},
		{http.MethodPost, "/api/v1/admin/charlie/trigger-events/" + eventID + "/retry/"},
		{http.MethodGet, "/api/v1/admin/charlie/access/"},
		{http.MethodPut, "/api/v1/admin/charlie/access/"},
		{http.MethodPost, "/api/v1/admin/charlie/diagnostics/run/"},
	}
	for _, request := range requests {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			req := httptest.NewRequest(request.method, request.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
			}
		})
	}
}
