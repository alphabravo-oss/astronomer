package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// UserQuerier abstracts the user-related database queries needed by AuthHandler.
// This allows for easy testing with mock implementations.
type UserQuerier interface {
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	UpdateUserLastLogin(ctx context.Context, id uuid.UUID) error
}

// AuthAuditWriter is the optional audit-writer dependency for AuthHandler.
// Wired separately because UserQuerier is used by tests with narrow fakes
// that do not (and should not) implement CreateAuditLog. Auth-handler is
// also unique in that it must record a user_id on success but accept an
// anonymous (NULL) user_id on failed login.
type AuthAuditWriter interface {
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// PasswordRehasher updates a user's password column. It is satisfied by the
// generated sqlc Queries type via UpdateUserPasswordHash and is used by the
// login handler to opportunistically migrate Django-format hashes to bcrypt.
type PasswordRehasher interface {
	UpdateUserPasswordHash(ctx context.Context, arg sqlc.UpdateUserPasswordHashParams) error
}

// RoleBindingsQuerier supplies the aggregated role bindings rendered into
// /api/v1/auth/me/. It is implemented by the generated sqlc Queries type.
type RoleBindingsQuerier interface {
	GetGlobalRoleBindingsByUserID(ctx context.Context, userID pgtype.UUID) ([]sqlc.GlobalRoleBinding, error)
	GetClusterRoleBindingsByUserID(ctx context.Context, userID pgtype.UUID) ([]sqlc.ClusterRoleBinding, error)
	GetProjectRoleBindingsByUserID(ctx context.Context, userID pgtype.UUID) ([]sqlc.ProjectRoleBinding, error)
	GetGlobalRoleByID(ctx context.Context, id uuid.UUID) (sqlc.GlobalRole, error)
	GetClusterRoleByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRole, error)
	GetProjectRoleByID(ctx context.Context, id uuid.UUID) (sqlc.ProjectRole, error)
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
	queries  UserQuerier
	tokens   TokenQuerier
	rehasher PasswordRehasher
	roles    RoleBindingsQuerier
	audit    AuthAuditWriter
	jwt      *auth.JWTManager
	log      *slog.Logger
}

// NewAuthHandler creates a new auth handler.
func NewAuthHandler(queries UserQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		jwt:     jwt,
		log:     slog.Default(),
	}
}

// NewAuthHandlerWithTokens creates a new auth handler with token support.
func NewAuthHandlerWithTokens(queries UserQuerier, tokens TokenQuerier, jwt *auth.JWTManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		tokens:  tokens,
		jwt:     jwt,
		log:     slog.Default(),
	}
}

// SetPasswordRehasher attaches the rehash hook used by Login() to upgrade
// inherited Django PBKDF2/argon2 hashes to bcrypt on first successful match.
func (h *AuthHandler) SetPasswordRehasher(p PasswordRehasher) {
	h.rehasher = p
}

// SetRoleBindings attaches the queries used by /auth/me/ to surface a user's
// aggregated global/cluster/project role bindings.
func (h *AuthHandler) SetRoleBindings(r RoleBindingsQuerier) {
	h.roles = r
}

// SetAuditWriter wires the audit-log writer used by Login / Logout /
// ChangePassword. Optional; when nil, auth events are not persisted (which
// matches the existing behaviour of the test fakes that don't supply it).
func (h *AuthHandler) SetAuditWriter(a AuthAuditWriter) {
	h.audit = a
}

// SetLogger overrides the handler's logger.
func (h *AuthHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
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

type refreshRequest struct {
	Refresh string `json:"refresh"`
}

func userToResponse(user sqlc.User) UserResponse {
	var lastLogin *string
	if user.LastLogin.Valid {
		s := user.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
		lastLogin = &s
	}

	return UserResponse{
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
	}
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

	// The frontend form is labelled "Email" but submits its value in the
	// `username` field (legacy axios contract). Accept either: try email lookup
	// when the value contains '@', otherwise try username, then fall back to
	// the other shape so users can log in with either credential.
	identifier := req.Username
	if req.Email != "" {
		identifier = req.Email
	}
	if strings.Contains(identifier, "@") {
		user, err = h.queries.GetUserByEmail(ctx, identifier)
		if err != nil {
			user, err = h.queries.GetUserByUsername(ctx, identifier)
		}
	} else {
		user, err = h.queries.GetUserByUsername(ctx, identifier)
		if err != nil {
			user, err = h.queries.GetUserByEmail(ctx, identifier)
		}
	}

	if err != nil {
		// User-not-found is recorded under the attempted identifier so a
		// brute-force scan for valid usernames is visible in the audit
		// stream. user_id stays NULL because there's no row to attribute.
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.login_failed", "user", "", identifier, map[string]any{
			"reason": "user_not_found",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	ok, needsRehash, verifyErr := auth.VerifyPassword(user.Password, req.Password)
	if verifyErr != nil {
		// A malformed stored hash is treated as a credential failure to avoid
		// leaking schema details. The error is logged for operators.
		if h.log != nil {
			h.log.Warn("password verification error", "user_id", user.ID.String(), "error", verifyErr)
		}
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{
			"reason": "verify_error",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}
	if !ok {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{
			"reason": "bad_password",
		})
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials")
		return
	}

	if !user.IsActive {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.login_failed", "user", user.ID.String(), user.Username, map[string]any{
			"reason": "account_disabled",
		})
		RespondError(w, http.StatusForbidden, "account_disabled", "Account is disabled")
		return
	}

	// Opportunistically upgrade legacy Django PBKDF2/argon2 hashes to bcrypt.
	// Failure here is non-fatal — we still log the user in.
	if needsRehash && h.rehasher != nil {
		if newHash, hashErr := auth.HashPassword(req.Password); hashErr == nil {
			if err := h.rehasher.UpdateUserPasswordHash(ctx, sqlc.UpdateUserPasswordHashParams{
				ID:       user.ID,
				Password: newHash,
			}); err != nil && h.log != nil {
				h.log.Warn("failed to rehash password", "user_id", user.ID.String(), "error", err)
			}
		} else if h.log != nil {
			h.log.Warn("failed to compute bcrypt hash for rehash", "user_id", user.ID.String(), "error", hashErr)
		}
	}

	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	// Update last_login (best-effort; don't fail the login if this errors)
	_ = h.queries.UpdateUserLastLogin(ctx, user.ID)

	resp := LoginResponse{
		Token:   accessToken,
		Refresh: refreshToken,
		User:    userToResponse(user),
	}

	recordAuditAs(r, h.audit, pgtype.UUID{Bytes: user.ID, Valid: true},
		"auth.login", "user", user.ID.String(), user.Username, map[string]any{
			"identifier_type": loginIdentifierType(identifier),
		},
	)

	RespondJSON(w, http.StatusOK, resp)
}

// loginIdentifierType labels how the user logged in (email vs username) for
// the audit detail. Pure presentation; no security boundary.
func loginIdentifierType(identifier string) string {
	if strings.Contains(identifier, "@") {
		return "email"
	}
	return "username"
}

// Refresh handles POST /api/v1/auth/refresh/.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	claims, err := h.jwt.ValidateToken(req.Refresh)
	if err != nil || claims.TokenType != auth.RefreshToken {
		RespondError(w, http.StatusUnauthorized, "invalid_token", "Invalid refresh token")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	if err != nil || !user.IsActive {
		RespondError(w, http.StatusUnauthorized, "invalid_token", "Invalid refresh token")
		return
	}

	accessToken, refreshToken, err := h.jwt.GenerateTokenPair(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "token_error", "Failed to generate token")
		return
	}

	RespondJSON(w, http.StatusOK, map[string]string{
		"token":   accessToken,
		"refresh": refreshToken,
	})
}

// Logout handles POST /api/v1/auth/logout/.
//
// JWTs are stateless on the server, so logout is a no-op server-side: the
// frontend discards the token. We still expose this endpoint so the frontend's
// logout call doesn't 404 and to keep parity with the Python implementation,
// which returned a {"detail":"..."} envelope.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		recordAudit(r, h.audit, "auth.logout", "user", user.ID, user.Username, nil)
	}
	RespondJSONUnwrapped(w, http.StatusOK, map[string]string{"detail": "Logged out"})
}

// ChangePasswordRequest is the body for POST /api/v1/auth/change-password/.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /api/v1/auth/change-password/.
//
// Verifies the caller's current password, hashes the new one with bcrypt, and
// persists it via UpdateUserPasswordHash. Requires the auth middleware to have
// populated the request context with the authenticated user.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	authUser, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(authUser.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	var req ChangePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		RespondError(w, http.StatusBadRequest, "validation_error", "current_password and new_password are required")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	verified, _, verifyErr := auth.VerifyPassword(dbUser.Password, req.CurrentPassword)
	if verifyErr != nil || !verified {
		RespondError(w, http.StatusUnauthorized, "invalid_credentials", "Current password is incorrect")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "hash_error", "Failed to hash new password")
		return
	}

	if h.rehasher == nil {
		RespondError(w, http.StatusServiceUnavailable, "not_configured", "Password updates are not configured")
		return
	}
	if err := h.rehasher.UpdateUserPasswordHash(r.Context(), sqlc.UpdateUserPasswordHashParams{
		ID:       userID,
		Password: newHash,
	}); err != nil {
		RespondError(w, http.StatusInternalServerError, "update_error", "Failed to update password")
		return
	}

	recordAudit(r, h.audit, "auth.change_password", "user", dbUser.ID.String(), dbUser.Username, nil)

	RespondJSONUnwrapped(w, http.StatusOK, map[string]string{"detail": "Password updated"})
}

// CurrentUser handles GET /api/v1/auth/me/.
func (h *AuthHandler) CurrentUser(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok {
		RespondError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "internal_error", "Invalid user ID")
		return
	}

	dbUser, err := h.queries.GetUserByID(r.Context(), userID)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}

	resp := map[string]any{
		"id":           dbUser.ID.String(),
		"email":        dbUser.Email,
		"username":     dbUser.Username,
		"first_name":   dbUser.FirstName,
		"last_name":    dbUser.LastName,
		"is_active":    dbUser.IsActive,
		"is_staff":     dbUser.IsStaff,
		"is_superuser": dbUser.IsSuperuser,
		"date_joined":  dbUser.DateJoined.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if dbUser.LastLogin.Valid {
		resp["last_login"] = dbUser.LastLogin.Time.UTC().Format("2006-01-02T15:04:05Z")
	} else {
		resp["last_login"] = nil
	}
	resp["roles"] = h.collectRoles(r.Context(), userID)

	RespondJSON(w, http.StatusOK, resp)
}

// collectRoles returns the user's aggregated global/cluster/project role
// bindings in the same shape as the Python /auth/me/ response. The map is
// always populated with empty slices when the role querier is unconfigured.
func (h *AuthHandler) collectRoles(ctx context.Context, userID uuid.UUID) map[string]any {
	out := map[string]any{
		"global":  []any{},
		"cluster": []any{},
		"project": []any{},
	}
	if h.roles == nil {
		return out
	}
	pgID := pgtype.UUID{Bytes: userID, Valid: true}

	if globals, err := h.roles.GetGlobalRoleBindingsByUserID(ctx, pgID); err == nil {
		items := make([]map[string]any, 0, len(globals))
		for _, b := range globals {
			role, _ := h.roles.GetGlobalRoleByID(ctx, b.RoleID)
			items = append(items, map[string]any{
				"id":         b.ID.String(),
				"role_id":    b.RoleID.String(),
				"role_name":  role.Name,
				"role_rules": json.RawMessage(role.Rules),
				"group":      b.Group,
			})
		}
		out["global"] = items
	} else if h.log != nil {
		h.log.Warn("failed to load global role bindings", "error", err)
	}

	if clusters, err := h.roles.GetClusterRoleBindingsByUserID(ctx, pgID); err == nil {
		items := make([]map[string]any, 0, len(clusters))
		for _, b := range clusters {
			role, _ := h.roles.GetClusterRoleByID(ctx, b.RoleID)
			items = append(items, map[string]any{
				"id":         b.ID.String(),
				"role_id":    b.RoleID.String(),
				"role_name":  role.Name,
				"role_rules": json.RawMessage(role.Rules),
				"cluster_id": b.ClusterID.String(),
				"group":      b.Group,
			})
		}
		out["cluster"] = items
	} else if h.log != nil {
		h.log.Warn("failed to load cluster role bindings", "error", err)
	}

	if projects, err := h.roles.GetProjectRoleBindingsByUserID(ctx, pgID); err == nil {
		items := make([]map[string]any, 0, len(projects))
		for _, b := range projects {
			role, _ := h.roles.GetProjectRoleByID(ctx, b.RoleID)
			items = append(items, map[string]any{
				"id":         b.ID.String(),
				"role_id":    b.RoleID.String(),
				"role_name":  role.Name,
				"role_rules": json.RawMessage(role.Rules),
				"project_id": b.ProjectID.String(),
				"group":      b.Group,
			})
		}
		out["project"] = items
	} else if h.log != nil {
		h.log.Warn("failed to load project role bindings", "error", err)
	}

	return out
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
