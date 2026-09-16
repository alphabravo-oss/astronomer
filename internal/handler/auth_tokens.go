package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- API Token CRUD ---

// CreateTokenRequest represents the request body for creating an API token.
// openapi:request-operation postAuthTokens
// openapi:request-operation postSettingsTokens
type CreateTokenRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays int      `json:"expires_in_days"`
	Scopes        []string `json:"scopes"`
	// AllowedCIDRs is a comma-separated CIDR list (migration 044). Empty
	// preserves the pre-044 behaviour (no IP restriction). Both bare
	// IPv4/IPv6 and CIDR forms are accepted; we re-serialise into the
	// stored canonical form before persisting.
	AllowedCIDRs string `json:"allowed_cidrs"`
}

// CreateTokenResponse is returned once after token creation, including the plaintext.
type CreateTokenResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Token        string   `json:"token"`
	Prefix       string   `json:"prefix"`
	ExpiresAt    *string  `json:"expires_at"`
	CreatedAt    string   `json:"created_at"`
	Scopes       []string `json:"scopes"`
	AllowedCIDRs string   `json:"allowed_cidrs"`
}

// TokenListItem is a single token in the list response (no plaintext).
type TokenListItem struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Prefix           string   `json:"prefix"`
	ExpiresAt        *string  `json:"expires_at"`
	LastUsedAt       *string  `json:"last_used_at"`
	IsRevoked        bool     `json:"is_revoked"`
	CreatedAt        string   `json:"created_at"`
	Scopes           []string `json:"scopes"`
	AllowedCIDRs     string   `json:"allowed_cidrs"`
	LastSeenRemoteIP string   `json:"last_seen_remote_ip"`
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
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Token name is required")
		return
	}

	plaintext, tokenHash, prefix, err := generateAPIToken()
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenGenerationError, "Failed to generate token")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	// Per-user token cap (migration 051). Soft enforcement allows the
	// create but emits a metric; hard returns a 429 + structured body.
	if h.enforcer != nil {
		if err := h.enforcer.CheckUserTokenCreate(r.Context(), userID); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate token quota")
			return
		}
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

	// Validate the CIDR list up-front so an operator typo fails the
	// CREATE with a 400 instead of a silent allow-everything row.
	// Empty string is the legacy "no IP restriction" mode and skips
	// the check entirely. We persist the user's raw string so the
	// CRUD UI round-trips byte-identically; parsing happens at auth
	// time.
	allowedCIDRs := strings.TrimSpace(req.AllowedCIDRs)
	if allowedCIDRs != "" {
		if _, perr := auth.ParseAllowedCIDRs(allowedCIDRs); perr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
				"allowed_cidrs must be a comma-separated list of valid CIDR ranges or IP addresses")

			return
		}
	}

	params := sqlc.CreateAPITokenParams{
		UserID:       userID,
		Name:         req.Name,
		TokenHash:    tokenHash,
		Prefix:       prefix,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
		AllowedCidrs: allowedCIDRs,
	}
	cidrCount := 0
	if allowedCIDRs != "" {
		cidrCount = strings.Count(allowedCIDRs, ",") + 1
	}
	token, err := executeMutation(r, h.runTx,
		func(q AuthMutationTx) (sqlc.ApiToken, error) { return q.CreateAPIToken(r.Context(), params) },
		func(token sqlc.ApiToken) mutationAuditEvent {
			return mutationAuditEvent{
				action: "auth.token.create", resourceType: "api_token", resourceID: token.ID.String(), resourceName: token.Name,
				status: http.StatusCreated,
				detail: map[string]any{
					"prefix": token.Prefix, "expires_in_days": req.ExpiresInDays,
					"scopes": req.Scopes, "allowed_cidr_count": cidrCount,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create token")
		return
	}

	// Security-FYI email — "a new token was issued; if this wasn't
	// you...". Best-effort, never blocks the response.
	if h.emails != nil {
		// Look up the user row for the email; the middleware-supplied
		// `user` shape only carries ID/username/email summaries.
		if u, err := h.queries.GetUserByID(r.Context(), userID); err == nil && u.Email != "" {
			h.emails.EnqueueAndLog(r.Context(), EmailNotifierRequest{
				To:       u.Email,
				Template: "api_token_created",
				Data: map[string]any{
					"Username":    u.Username,
					"TokenName":   token.Name,
					"TokenPrefix": token.Prefix,
					"CreatedAt":   token.CreatedAt.UTC().Format(time.RFC3339),
				},
				UserID: userID,
			})
		}
	}

	var expiresAtStr *string
	if token.ExpiresAt.Valid {
		s := token.ExpiresAt.Time.UTC().Format("2006-01-02T15:04:05Z")
		expiresAtStr = &s
	}

	respScopes := req.Scopes
	if respScopes == nil {
		respScopes = []string{}
	}
	w.Header().Set("Location", "/api/v1/auth/tokens/"+token.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, CreateTokenResponse{
		ID:           token.ID.String(),
		Name:         token.Name,
		Token:        plaintext,
		Prefix:       token.Prefix,
		ExpiresAt:    expiresAtStr,
		CreatedAt:    token.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Scopes:       respScopes,
		AllowedCIDRs: token.AllowedCidrs,
	})
}

// ListTokens handles GET /api/v1/auth/tokens/.
func (h *AuthHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	tokens, err := h.tokens.ListTokensByUser(r.Context(), sqlc.ListTokensByUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list tokens")
		return
	}

	total, err := h.tokens.CountTokensByUser(r.Context(), userID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count tokens")
		return
	}

	items := make([]TokenListItem, 0, len(tokens))
	for _, t := range tokens {
		scopes, _ := auth.ParseTokenScopes(t.Scopes)
		if scopes == nil {
			scopes = []string{}
		}
		item := TokenListItem{
			ID:               t.ID.String(),
			Name:             t.Name,
			Prefix:           t.Prefix,
			IsRevoked:        t.IsRevoked,
			CreatedAt:        t.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Scopes:           scopes,
			AllowedCIDRs:     t.AllowedCidrs,
			LastSeenRemoteIP: t.LastSeenRemoteIp,
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

	paging.Write(w, items, paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(items)))
}

// RevokeToken handles DELETE /api/v1/auth/tokens/{id}/.
func (h *AuthHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}

	if h.tokens == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.NotConfigured, "Token management is not configured")
		return
	}

	tokenIDStr := chi.URLParam(r, "id")
	tokenID, err := uuid.Parse(tokenIDStr)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid token ID")
		return
	}

	// Verify the token belongs to the authenticated user.
	token, err := h.tokens.GetAPITokenByID(r.Context(), tokenID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Token not found")
		return
	}

	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Invalid user ID")
		return
	}

	if token.UserID != userID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Token not found")
		return
	}

	_, err = executeMutation(r, h.runTx,
		func(q AuthMutationTx) (sqlc.ApiToken, error) {
			return token, q.RevokeAPIToken(r.Context(), tokenID)
		},
		func(token sqlc.ApiToken) mutationAuditEvent {
			return mutationAuditEvent{
				action: "auth.token.revoke", resourceType: "api_token", resourceID: token.ID.String(), resourceName: token.Name,
				status: http.StatusNoContent, detail: map[string]any{"prefix": token.Prefix},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RevokeError, "Failed to revoke token")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
