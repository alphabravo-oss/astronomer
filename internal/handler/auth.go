package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries UserQuerier
	jwt     *auth.JWTManager
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries UserQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		jwt:     jwt,
	}
}

// LoginRequest represents the login request body.
type LoginRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UserResponse represents the user data in login response.
type UserResponse struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	Username    string  `json:"username"`
	FirstName   string  `json:"first_name"`
	LastName    string  `json:"last_name"`
	IsActive    bool    `json:"is_active"`
	IsStaff     bool    `json:"is_staff"`
	IsSuperuser bool    `json:"is_superuser"`
	DateJoined  string  `json:"date_joined"`
	LastLogin   *string `json:"last_login"`
}

// LoginResponse matches the Python AstronomerTokenObtainPairSerializer.
type LoginResponse struct {
	Token   string       `json:"token"`
	Refresh string       `json:"refresh"`
	User    UserResponse `json:"user"`
}

// Login handles POST /api/v1/auth/login/.
// Accepts {username, password} or {email, password}.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Email == "" && req.Username == "" {
		RespondError(w, http.StatusBadRequest, "missing_credentials", "Email or username is required")
		return
	}

	if req.Password == "" {
		RespondError(w, http.StatusBadRequest, "missing_credentials", "Password is required")
		return
	}

	ctx := r.Context()
	var user sqlc.User
	var err error

	if req.Email != "" {
		user, err = h.queries.GetUserByEmail(ctx, req.Email)
	} else {
		user, err = h.queries.GetUserByUsername(ctx, req.Username)
	}

	if err != nil {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	if !user.IsActive {
		RespondError(w, http.StatusForbidden, "account_disabled", "Account is disabled")
		return
	}

	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	// Update last_login (best-effort; don't fail the login if this errors)
	_ = h.queries.UpdateUserLastLogin(ctx, user.ID)

	var lastLogin *string
	if user.LastLogin.Valid {
		s := user.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastLogin = &s
	}

	resp := LoginResponse{
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
	}

	RespondJSON(w, http.StatusOK, resp)
}
