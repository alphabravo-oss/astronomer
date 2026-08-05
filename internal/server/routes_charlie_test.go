package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type charlieEnabledSettings struct{}

func (charlieEnabledSettings) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	return sqlc.PlatformSetting{Key: key, Value: json.RawMessage("true")}, nil
}

func TestAPIRequestTimeoutExemptsOnlyCharlieEventStreams(t *testing.T) {
	handler := apiRequestTimeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline := r.Context().Deadline()
		_ = json.NewEncoder(w).Encode(map[string]bool{"deadline": hasDeadline})
	}))
	for _, test := range []struct {
		method, path string
		deadline     bool
	}{
		{http.MethodGet, "/api/v1/charlie/sessions/session-a/events/", false},
		{http.MethodGet, "/api/v1/charlie/sessions/session-a/history/", true},
		{http.MethodPost, "/api/v1/charlie/sessions/session-a/events/", true},
		{http.MethodGet, "/api/v1/clusters/", true},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		var response map[string]bool
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["deadline"] != test.deadline {
			t.Fatalf("%s %s deadline=%v, want %v", test.method, test.path, response["deadline"], test.deadline)
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
		{http.MethodPost, "/api/v1/admin/charlie/agent/install/"},
		{http.MethodPost, "/api/v1/admin/charlie/agent/upgrade/"},
		{http.MethodPost, "/api/v1/admin/charlie/agent/rollback/"},
		{http.MethodPost, "/api/v1/admin/charlie/agent/rotate/"},
		{http.MethodPost, "/api/v1/admin/charlie/agent/uninstall/"},
		{http.MethodPatch, "/api/v1/admin/charlie/mode/"},
		{http.MethodGet, "/api/v1/admin/charlie/trigger-rules/"},
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
