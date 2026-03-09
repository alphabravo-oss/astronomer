package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// TokenQuerier abstracts the API token database queries needed by AuthHandler.
type TokenQuerier interface {
	CreateAPIToken(ctx context.Context, arg sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	ListTokensByUser(ctx context.Context, arg sqlc.ListTokensByUserParams) ([]sqlc.ApiToken, error)
	CountTokensByUser(ctx context.Context, userID uuid.UUID) (int64, error)
	GetAPITokenByID(ctx context.Context, id uuid.UUID) (sqlc.ApiToken, error)
	RevokeAPIToken(ctx context.Context, id uuid.UUID) error
}

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	queries UserQuerier
	tokens  TokenQuerier
	jwt     *auth.JWTManager
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries UserQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		jwt:     jwt,
	}
}

// NewAuthHandlerWithTokens creates a new auth handler with token support.
func NewAuthHandlerWithTokens(queries UserQuerier, tokens TokenQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		tokens:  tokens,
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

// --- API Token CRUD ---

// CreateTokenRequest represents the request body for creating an API token.
type CreateTokenRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays int      `json:"expires_in_days"`
	Scopes        []string `json:"scopes"`
}

// CreateTokenResponse is returned once after token creation, including the plaintext.
type CreateTokenResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Token     string  `json:"token"`
	Prefix    string  `json:"prefix"`
	ExpiresAt *string `json:"expires_at"`
	CreatedAt string  `json:"created_at"`
}

// TokenListItem is a single token in the list response (no plaintext).
type TokenListItem struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	IsRevoked  bool    `json:"is_revoked"`
	CreatedAt  string  `json:"created_at"`
}

// generateAPIToken creates a random API token with prefix, hash, and prefix string.
func generateAPIToken() (plaintext, hash, prefix string, err error) {
	b := make([]byte, 48)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = "astro_" + base64.URLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	prefix = plaintext[:12]
	return plaintext, hash, prefix, nil
}

// CreateToken handles POST /api/v1/auth/tokens/.
func (h *AuthHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "Token name is required")
		return
	}

	plaintext, tokenHash, prefix, err := generateAPIToken()
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_generation_error", "Failed to generate token")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var expiresAt pgtype.Timestamptz
	if req.ExpiresInDays > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour),
			Valid: true,
		}
	}

	scopes, _ := json.Marshal(req.Scopes)
	if req.Scopes == nil {
		scopes = json.RawMessage(`[]`)
	}

	token, err := h.tokens.CreateAPIToken(r.Context(), sqlc.CreateAPITokenParams{
		UserID:    userID,
		Name:      req.Name,
		TokenHash: tokenHash,
		Prefix:    prefix,
		ExpiresAt: expiresAt,
		Scopes:    scopes,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "create_error", "Failed to create token")
		return
	}

	var expiresAtStr *string
	if token.ExpiresAt.Valid {
		s := token.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		expiresAtStr = &s
	}

	RespondJSON(w, http.StatusCreated, CreateTokenResponse{
		ID:        token.ID.String(),
		Name:      token.Name,
		Token:     plaintext,
		Prefix:    token.Prefix,
		ExpiresAt: expiresAtStr,
		CreatedAt: token.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// ListTokens handles GET /api/v1/auth/tokens/.
func (h *AuthHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	tokens, err := h.tokens.ListTokensByUser(r.Context(), sqlc.ListTokensByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "list_error", "Failed to list tokens")
		return
	}

	total, err := h.tokens.CountTokensByUser(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "count_error", "Failed to count tokens")
		return
	}

	items := make([]TokenListItem, 0, len(tokens))
	for _, t := range tokens {
		item := TokenListItem{
			ID:        t.ID.String(),
			Name:      t.Name,
			Prefix:    t.Prefix,
			IsRevoked: t.IsRevoked,
			CreatedAt: t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
		if t.ExpiresAt.Valid {
			s := t.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			item.ExpiresAt = &s
		}
		if t.LastUsedAt.Valid {
			s := t.LastUsedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
			item.LastUsedAt = &s
		}
		items = append(items, item)
	}

	RespondPaginated(w, r, items, total)
}

// RevokeToken handles DELETE /api/v1/auth/tokens/{id}/.
func (h *AuthHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondError(w, http.StatusInternalServerError, "not_configured", "Token management is not configured")
		return
	}

	tokenIDStr := chi.URLParam(r, "id")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid token ID")
		return
	}

	// Verify the token belongs to the authenticated user.
	token, err := h.tokens.GetAPITokenByID(r.Context(), tokenID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "Token not found")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	if token.UserID != userID {
		RespondError(w, http.StatusNotFound, "not_found", "Token not found")
		return
	}

	if err := h.tokens.RevokeAPIToken(r.Context(), tokenID); err != nil {
		RespondError(w, http.StatusInternalServerError, "revoke_error", "Failed to revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
