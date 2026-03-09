package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// mockBootstrapQuerier implements BootstrapQuerier for testing.
type mockBootstrapQuerier struct {
	userCount      int64
	platformConfig *sqlc.PlatformConfiguration
	createdUser    *sqlc.User
	createUserErr  error
}

func (m *mockBootstrapQuerier) GetPlatformConfig(_ context.Context) (sqlc.PlatformConfiguration, error) {
	if m.platformConfig != nil {
		return *m.platformConfig, nil
	}
	return sqlc.PlatformConfiguration{}, fmt.Errorf("no rows in result set")
}

func (m *mockBootstrapQuerier) UpsertPlatformConfig(_ context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error) {
	cfg := sqlc.PlatformConfiguration{
		ID:               1,
		ServerUrl:        arg.ServerUrl,
		PlatformName:     arg.PlatformName,
		TelemetryEnabled: arg.TelemetryEnabled,
		BootstrappedAt:   arg.BootstrappedAt,
	}
	m.platformConfig = &cfg
	return cfg, nil
}

func (m *mockBootstrapQuerier) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	if m.createUserErr != nil {
		return sqlc.User{}, m.createUserErr
	}
	user := sqlc.User{
		ID:          uuid.New(),
		Email:       arg.Email,
		Username:    arg.Username,
		FirstName:   arg.FirstName,
		LastName:    arg.LastName,
		Password:    arg.Password,
		IsActive:    arg.IsActive,
		IsStaff:     arg.IsStaff,
		IsSuperuser: arg.IsSuperuser,
		DateJoined:  time.Now().UTC(),
	}
	m.createdUser = &user
	m.userCount++
	return user, nil
}

func (m *mockBootstrapQuerier) CountUsers(_ context.Context) (int64, error) {
	return m.userCount, nil
}

func TestBootstrapGetStatus_NotBootstrapped(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{userCount: 0}
	h := NewBootstrapHandler(mock, jwtMgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/", nil)
	w := httptest.NewRecorder()

	h.GetBootstrapStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'data' wrapper, got: %v", body)
	}

	if data["bootstrapped"] != false {
		t.Fatalf("expected bootstrapped=false, got %v", data["bootstrapped"])
	}
	if data["platform_name"] != "Astronomer" {
		t.Fatalf("expected platform_name=Astronomer, got %v", data["platform_name"])
	}
}

func TestBootstrapGetStatus_Bootstrapped(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{
		userCount: 1,
		platformConfig: &sqlc.PlatformConfiguration{
			ID:               1,
			ServerUrl:        "https://example.com",
			PlatformName:     "My Platform",
			TelemetryEnabled: true,
			BootstrappedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}
	h := NewBootstrapHandler(mock, jwtMgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap/", nil)
	w := httptest.NewRecorder()

	h.GetBootstrapStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'data' wrapper, got: %v", body)
	}

	if data["bootstrapped"] != true {
		t.Fatalf("expected bootstrapped=true, got %v", data["bootstrapped"])
	}
	if data["server_url"] != "https://example.com" {
		t.Fatalf("expected server_url=https://example.com, got %v", data["server_url"])
	}
	if data["platform_name"] != "My Platform" {
		t.Fatalf("expected platform_name=My Platform, got %v", data["platform_name"])
	}
}

func TestBootstrapComplete_Success(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{userCount: 0}
	h := NewBootstrapHandler(mock, jwtMgr)

	reqBody := CompleteBootstrapRequest{
		Email:        "admin@example.com",
		Username:     "admin",
		Password:     "securepassword123",
		FirstName:    "Admin",
		LastName:     "User",
		ServerURL:    "https://astronomer.example.com",
		PlatformName: "My Astronomer",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap/complete/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CompleteBootstrap(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'data' wrapper, got: %v", body)
	}

	if data["token"] == nil || data["token"] == "" {
		t.Fatal("expected non-empty token")
	}
	if data["refresh"] == nil || data["refresh"] == "" {
		t.Fatal("expected non-empty refresh token")
	}

	user, ok := data["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'user' object, got: %v", data)
	}
	if user["email"] != "admin@example.com" {
		t.Fatalf("expected email=admin@example.com, got %v", user["email"])
	}
	if user["username"] != "admin" {
		t.Fatalf("expected username=admin, got %v", user["username"])
	}
	if user["is_superuser"] != true {
		t.Fatalf("expected is_superuser=true, got %v", user["is_superuser"])
	}
	if user["is_staff"] != true {
		t.Fatalf("expected is_staff=true, got %v", user["is_staff"])
	}

	platform, ok := data["platform"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'platform' object, got: %v", data)
	}
	if platform["server_url"] != "https://astronomer.example.com" {
		t.Fatalf("expected server_url=https://astronomer.example.com, got %v", platform["server_url"])
	}
}

func TestBootstrapComplete_AlreadyBootstrapped(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{userCount: 1}
	h := NewBootstrapHandler(mock, jwtMgr)

	reqBody := CompleteBootstrapRequest{
		Email:    "admin@example.com",
		Password: "securepassword123",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap/complete/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CompleteBootstrap(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got: %v", body)
	}
	if errObj["code"] != "already_bootstrapped" {
		t.Fatalf("expected error code already_bootstrapped, got %v", errObj["code"])
	}
}

func TestBootstrapComplete_PasswordTooShort(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{userCount: 0}
	h := NewBootstrapHandler(mock, jwtMgr)

	reqBody := CompleteBootstrapRequest{
		Email:    "admin@example.com",
		Password: "short",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap/complete/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CompleteBootstrap(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got: %v", body)
	}
	if errObj["code"] != "validation_error" {
		t.Fatalf("expected error code validation_error, got %v", errObj["code"])
	}
}

func TestBootstrapComplete_MissingEmail(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key-for-testing", 60)
	mock := &mockBootstrapQuerier{userCount: 0}
	h := NewBootstrapHandler(mock, jwtMgr)

	reqBody := CompleteBootstrapRequest{
		Password: "securepassword123",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/bootstrap/complete/", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.CompleteBootstrap(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d; body: %s", w.Code, w.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got: %v", body)
	}
	if errObj["code"] != "validation_error" {
		t.Fatalf("expected error code validation_error, got %v", errObj["code"])
	}
}
