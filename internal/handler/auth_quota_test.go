package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
)

// quotaEnforcerFakeQuerier is the smallest QuotaQuerier the enforcer
// needs for the AuthHandler.CreateToken test below. It directly maps
// per-user token counts to whatever the test seeds.
type quotaEnforcerFakeQuerier struct {
	plan   sqlc.GetEffectiveQuotaForUserRow
	tokens int64
}

func (q *quotaEnforcerFakeQuerier) GetQuotaPlan(_ context.Context, _ string) (sqlc.QuotaPlan, error) {
	return sqlc.QuotaPlan{}, nil
}
func (q *quotaEnforcerFakeQuerier) GetEffectiveQuotaForUser(_ context.Context, _ uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error) {
	return q.plan, nil
}
func (q *quotaEnforcerFakeQuerier) GetEffectiveQuotaForProject(_ context.Context, _ uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error) {
	return sqlc.GetEffectiveQuotaForProjectRow{}, nil
}
func (q *quotaEnforcerFakeQuerier) CountClustersInProject(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *quotaEnforcerFakeQuerier) CountMembersInProject(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *quotaEnforcerFakeQuerier) CountProjectsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *quotaEnforcerFakeQuerier) CountActiveTokensForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return q.tokens, nil
}
func (q *quotaEnforcerFakeQuerier) CountTotalClusters(_ context.Context) (int64, error) {
	return 0, nil
}
func (q *quotaEnforcerFakeQuerier) CountTotalActiveUsers(_ context.Context) (int64, error) {
	return 0, nil
}

// TestAuthHandler_TokenCreateQuotaExceeded covers the 429 path on the
// per-user token cap. The enforcer is wired with a fake that reports
// "you already have 5 tokens, max is 5" → CreateToken must return 429.
func TestAuthHandler_TokenCreateQuotaExceeded(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	tokenQ := newMockTokenQuerier()
	handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
	enf := quota.New(&quotaEnforcerFakeQuerier{
		plan: sqlc.GetEffectiveQuotaForUserRow{
			UserID:           userID,
			PlanName:         "free",
			Enforcement:      "hard",
			MaxTokensPerUser: 5,
		},
		tokens: 5,
	}, nil)
	handler.SetQuotaEnforcer(enf)

	body := `{"name": "My Token", "expires_in_days": 90}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUser(req, userID.String())

	w := httptest.NewRecorder()
	handler.CreateToken(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	errMap, _ := resp["error"].(map[string]any)
	if errMap["code"] != "quota_exceeded" {
		t.Errorf("expected quota_exceeded code, got %v", errMap["code"])
	}
	if errMap["limit"] != "max_tokens_per_user" {
		t.Errorf("expected limit=max_tokens_per_user, got %v", errMap["limit"])
	}
}

// TestAuthHandler_TokenCreateAllowsUnderCap covers the happy path: the
// enforcer is wired but the user is well under the cap; the create
// proceeds with a normal 201.
func TestAuthHandler_TokenCreateAllowsUnderCap(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	userID := uuid.New()

	tokenQ := newMockTokenQuerier()
	handler := NewAuthHandlerWithTokens(newMockQuerier(), tokenQ, jwtMgr)
	enf := quota.New(&quotaEnforcerFakeQuerier{
		plan: sqlc.GetEffectiveQuotaForUserRow{
			UserID:           userID,
			PlanName:         "free",
			Enforcement:      "hard",
			MaxTokensPerUser: 5,
		},
		tokens: 0,
	}, nil)
	handler.SetQuotaEnforcer(enf)

	body := `{"name": "My Token", "expires_in_days": 90}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/tokens/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUser(req, userID.String())

	w := httptest.NewRecorder()
	handler.CreateToken(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}
