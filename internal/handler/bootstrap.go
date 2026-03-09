package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// BootstrapQuerier abstracts the database queries needed by BootstrapHandler.
type BootstrapQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(ctx context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	CountUsers(ctx context.Context) (int64, error)
}

// BootstrapHandler handles first-run setup endpoints.
type BootstrapHandler struct {
	queries BootstrapQuerier
	jwt     *auth.JWTManager
}

// NewBootstrapHandler creates a new bootstrap handler.
func NewBootstrapHandler(queries BootstrapQuerier, jwt *auth.JWTManager) *BootstrapHandler {
	return &BootstrapHandler{
		queries: queries,
		jwt:     jwt,
	}
}

// BootstrapStatusResponse is the response for GET /api/v1/bootstrap/.
type BootstrapStatusResponse struct {
	Bootstrapped bool   `json:"bootstrapped"`
	ServerURL    string `json:"server_url"`
	PlatformName string `json:"platform_name"`
}

// CompleteBootstrapRequest is the request body for POST /api/v1/bootstrap/complete/.
type CompleteBootstrapRequest struct {
	Email        string `json:"email"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	ServerURL    string `json:"server_url"`
	PlatformName string `json:"platform_name"`
}

// CompleteBootstrapResponse is the response for POST /api/v1/bootstrap/complete/.
type CompleteBootstrapResponse struct {
	Token    string                     `json:"token"`
	Refresh  string                     `json:"refresh"`
	User     UserResponse               `json:"user"`
	Platform sqlc.PlatformConfiguration `json:"platform"`
}

// GetBootstrapStatus handles GET /api/v1/bootstrap/.
// Public endpoint, no auth required.
func (h *BootstrapHandler) GetBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	resp := BootstrapStatusResponse{
		Bootstrapped: false,
		ServerURL:    "",
		PlatformName: "Astronomer",
	}

	platform, err := h.queries.GetPlatformConfig(ctx)
	if err == nil {
		resp.ServerURL = platform.ServerUrl
		resp.PlatformName = platform.PlatformName
		resp.Bootstrapped = platform.BootstrappedAt.Valid
	}

	RespondJSON(w, http.StatusOK, resp)
}

// CompleteBootstrap handles POST /api/v1/bootstrap/complete/.
// Public endpoint (only works if not yet bootstrapped).
func (h *BootstrapHandler) CompleteBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. Check if already bootstrapped
	count, err := h.queries.CountUsers(ctx)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "db_error", "Failed to check bootstrap status")
		return
	}
	if count > 0 {
		RespondError(w, http.StatusBadRequest, "already_bootstrapped", "Platform already bootstrapped")
		return
	}

	// 2. Parse and validate request
	var req CompleteBootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Email == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Email is required")
		return
	}

	if len(req.Password) < 12 {
		RespondError(w, http.StatusBadRequest, "validation_error", "Password must be at least 12 characters")
		return
	}

	if req.Username == "" {
		req.Username = req.Email
	}

	if req.PlatformName == "" {
		req.PlatformName = "Astronomer"
	}

	// 3. Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "hash_error", "Failed to hash password")
		return
	}

	// 4. Create admin user
	user, err := h.queries.CreateUser(ctx, sqlc.CreateUserParams{
		Email:       req.Email,
		Username:    req.Username,
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Password:    string(hashedPassword),
		IsActive:    true,
		IsStaff:     true,
		IsSuperuser: true,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_user_error", "Failed to create admin user")
		return
	}

	// 5. Upsert platform config
	now := time.Now().UTC()
	platform, err := h.queries.UpsertPlatformConfig(ctx, sqlc.UpsertPlatformConfigParams{
		ServerUrl:        req.ServerURL,
		PlatformName:     req.PlatformName,
		TelemetryEnabled: true,
		BootstrappedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "config_error", "Failed to save platform configuration")
		return
	}

	// 6. Generate JWT tokens
	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	// 7. Build response
	var lastLogin *string
	if user.LastLogin.Valid {
		s := user.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastLogin = &s
	}

	resp := CompleteBootstrapResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User: UserResponse{
			ID:          user.ID.String(),
			Email:       user.Email,
			Username:    user.Username,
			FirstName:   user.FirstName,
			LastName:    user.LastName,
			IsActive:    user.IsActive,
			IsStaff:     user.IsStaff,
			IsSuperuser: user.IsSuperuser,
			DateJoined:  user.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
			LastLogin:   lastLogin,
		},
		Platform: platform,
	}

	RespondJSON(w, http.StatusCreated, resp)
}
