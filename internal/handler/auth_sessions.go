package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	if strings.TrimSpace(req.Refresh) == "" {
		if c, err := r.Cookie(auth.RefreshCookieName); err == nil {
			if !auth.ValidateCSRF(r) {
				RespondRequestError(w, r, http.StatusUnauthorized, apierror.CSRFRequired, "CSRF token is required")
				return
			}
			req.Refresh = c.Value
		}
	}
	if strings.TrimSpace(req.Refresh) == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	claims, err := h.jwt.ValidateToken(req.Refresh)
	if err != nil {
		recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.refresh_failed", "user", "", "", map[string]any{
			"reason": "invalid_token",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}
	if claims.TokenType != auth.RefreshToken {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "wrong_token_type",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), claims.UserID)
	if err != nil || !user.IsActive {
		recordAuditAs(r, h.audit, pgtype.UUID{Bytes: claims.UserID, Valid: true}, "auth.refresh_failed", "user", claims.UserID.String(), "", map[string]any{
			"reason": "user_not_active",
		})
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid refresh token")
		return
	}

	if h.handleRefreshTOTP(w, r, user) {
		return
	}

	mintCtx := h.applySessionTimeoutFromSettings(r.Context())
	accessToken, refreshToken, err := h.jwt.GenerateTokenPairContext(mintCtx, user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate token")
		return
	}

	if auditErr := h.recordCredentialAuditAs(r, pgtype.UUID{Bytes: user.ID, Valid: true}, "auth.refresh", "user", user.ID.String(), user.Username, nil); auditErr != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the refreshed session was not issued")
		return
	}

	setBrowserSessionCookies(w, r, accessToken, refreshToken)
	RespondJSON(w, http.StatusOK, map[string]string{
		"token":   accessToken,
		"refresh": refreshToken,
	})
}

// Logout handles POST /api/v1/auth/logout/.
//
// JWTs are normally stateless on the server, but with the revocation
// layer wired we add the caller's JTI to the deny list so the
// no-longer-valid token can't be replayed before its natural expiry.
// When the revocation backend is unwired (tests / pre-DB bootstrap),
// the endpoint degrades back to the historical no-op shape: emit the
// audit row and return 200.
//
// Single sign-out (migration 054, NIST 800-53 AC-12 / SOC 2 CC6.6):
// when the caller's JTI has an sso_sessions row (i.e. they logged in
// via an upstream OIDC IdP), the response additionally carries a
// `redirect_url` pointing at the IdP's RP-initiated logout endpoint.
// The frontend follows that redirect so the upstream session is
// terminated too — local JWT revocation alone leaves the IdP's
// cookie intact, which a refresh would re-mint.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	authUser, ok := reqctx.AuthenticatedUser(r.Context())
	auditDetail := map[string]any{}

	// jtiForSLO carries the parsed JTI of the caller's access token
	// out of the revocation block so the SLO branch can look up the
	// matching sso_sessions row. Empty means "no bearer / no valid
	// JTI" → no SLO redirect.
	var jtiForSLO string
	// userIDForSLO carries the caller's user id out of the revocation block so
	// the SLO lookup can fall back to the newest sso_sessions row for the user
	// when the access JTI has rotated past a silent refresh (the row is keyed
	// on the login-time access JTI, which no longer matches).
	var userIDForSLO uuid.UUID
	auditRecorded := false

	// Extract the JTI from the bearer JWT so we can add THIS token's
	// JTI to the deny list. We don't trust the AuthenticatedUser to
	// carry it (the middleware doesn't propagate it today), so we
	// parse from the Authorization header.
	if h.revocation != nil && h.jwt != nil {
		if token := bearerTokenFromRequest(r); token != "" {
			if claims, validateErr := h.jwt.ValidateTokenContext(r.Context(), token); validateErr == nil {
				expiresAt := time.Time{}
				if claims.ExpiresAt != nil {
					expiresAt = claims.ExpiresAt.Time
				}
				if expiresAt.IsZero() {
					expiresAt = time.Now().Add(24 * time.Hour)
				}
				revokeParams := sqlc.RevokeJWTParams{Jti: claims.ID, UserID: claims.UserID, ExpiresAt: expiresAt, Reason: "user_logout"}
				invalidateParams := sqlc.InvalidateAllTokensParams{
					ID: claims.UserID, TokensInvalidatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				}
				result, mutationErr := executeMutation(r, h.runTx,
					func(q AuthMutationTx) (logoutMutationResult, error) {
						if err := q.RevokeJWT(r.Context(), revokeParams); err != nil {
							return logoutMutationResult{}, err
						}
						if err := q.InvalidateAllTokens(r.Context(), invalidateParams); err != nil {
							return logoutMutationResult{}, err
						}
						return logoutMutationResult{jti: claims.ID, userID: claims.UserID, expiresAt: expiresAt}, nil
					},
					func(result logoutMutationResult) mutationAuditEvent {
						resourceName := ""
						if authUser != nil {
							resourceName = authUser.Username
						}
						return mutationAuditEvent{
							action: "auth.logout", resourceType: "user", resourceID: result.userID.String(), resourceName: resourceName,
							status: http.StatusOK, detail: map[string]any{"jti": result.jti, "revoked": true, "all_tokens_invalidated": true},
						}
					})
				if mutationErr != nil {
					respondTransactionalMutationError(w, r, mutationErr, http.StatusServiceUnavailable, apierror.RevokeError,
						"Logout could not revoke the active session; retry")
					return
				}
				auth.SessionRevocationsTotal.WithLabelValues(observability.MetricValues("jti", "user_logout")...).Inc()
				auditDetail["jti"], auditDetail["revoked"] = result.jti, true
				jtiForSLO, userIDForSLO = result.jti, result.userID
				h.jwt.InvalidateJTI(r.Context(), result.jti)
				h.jwt.InvalidateUser(r.Context(), result.userID)
				auditRecorded = true
			}
		}
	}

	// Build the RP-initiated logout redirect when an upstream SSO
	// session is present for this JTI. The frontend follows the URL
	// with a top-level navigation so Dex (and any back-channel-SLO
	// connector behind it — SAML, certain OIDC providers) tears down
	// the user's session everywhere. Best-effort: every failure path
	// degrades to "no redirect_url" → local logout only.
	redirectURL := ""
	if jtiForSLO != "" {
		redirectURL = h.buildSSOLogoutRedirect(r, jtiForSLO, userIDForSLO, &auditDetail)
	}

	if !auditRecorded {
		actorID := pgtype.UUID{}
		resourceID, resourceName := "", ""
		if ok && authUser != nil {
			resourceID, resourceName = authUser.ID, authUser.Username
			if parsed, parseErr := uuid.Parse(authUser.ID); parseErr == nil {
				actorID = pgtype.UUID{Bytes: parsed, Valid: true}
			}
		}
		if auditErr := h.recordCredentialAuditAs(r, actorID, "auth.logout", "user", resourceID, resourceName, auditDetail); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; logout was not completed")
			return
		}
	}

	resp := map[string]any{"detail": "Logged out"}
	if redirectURL != "" {
		resp["redirect_url"] = redirectURL
	}
	clearBrowserSessionCookies(w, r)
	RespondJSONUnwrapped(w, http.StatusOK, resp)
}

// buildSSOLogoutRedirect looks up the upstream session for the caller's
// JTI, decrypts the stored id_token, and constructs the RP-initiated
// logout URL. Returns "" on every "skip this" path: no session row
// (local-password login), no end_session_endpoint advertised by the
// IdP, decrypt failure, or storage misconfigured. Each path emits its
// own audit + metric so an operator debugging "logout didn't kick me
// out of the IdP" can find the answer in the audit stream.
//
// Side effects: increments astronomer_auth_sso_logouts_total and
// deletes the sso_sessions row on every outcome (success OR fallback)
// because the JWT is already revoked and the stored id_token can't be
// reused safely.
func (h *AuthHandler) buildSSOLogoutRedirect(r *http.Request, jti string, userID uuid.UUID, auditDetail *map[string]any) string {
	if h == nil || h.ssoSessions == nil || h.encryptor == nil {
		return ""
	}
	session, err := h.ssoSessions.GetSSOSession(r.Context(), jti)
	if err != nil {
		// The row is keyed on the LOGIN-time access JTI; after a silent SPA
		// refresh the caller's current JTI no longer matches. Fall back to the
		// newest sso_sessions row for the user so single-sign-out still fires.
		// (id_token can't be re-minted on refresh, so we can't re-key the row.)
		if userID != (uuid.UUID{}) {
			if latest, lerr := h.ssoSessions.GetLatestSSOSessionByUser(r.Context(), userID); lerr == nil {
				session = latest
				jti = latest.Jti // delete the row we actually resolved
				err = nil
			}
		}
	}
	if err != nil {
		// sql.ErrNoRows is the dominant case here — local-password
		// users, or an SSO session row already cleaned up by a
		// concurrent admin force-logout. Either way: no redirect.
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues("", "no_session")...).Inc()
		return ""
	}
	if *auditDetail == nil {
		*auditDetail = map[string]any{}
	}
	(*auditDetail)["sso_provider"] = session.ProviderName

	// Drop the row regardless of whether we end up returning a
	// redirect URL: the JWT is already revoked, the row's id_token
	// will be useless once it expires, and leaving it lying around
	// just exposes the encrypted token to a later DB leak for no
	// benefit.
	defer func() {
		if err := h.ssoSessions.DeleteSSOSession(r.Context(), jti); err != nil && h.log != nil {
			h.log.Warn("failed to delete sso_sessions row", "jti", jti, "error", err)
		}
	}()

	if session.EndSessionEndpoint == "" {
		// IdP doesn't advertise RP-initiated logout (or discovery
		// failed at callback time). Local revocation is the best we
		// can do — record the gap so the SOC 2 dashboard sees it.
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "no_endpoint")...).Inc()
		(*auditDetail)["sso_logout"] = "no_endpoint"
		return ""
	}
	idToken, err := h.encryptor.Decrypt(session.UpstreamIDTokenEncrypted)
	if err != nil {
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "encrypt_error")...).Inc()
		(*auditDetail)["sso_logout"] = "decrypt_failed"
		if h.log != nil {
			h.log.Warn("failed to decrypt upstream id_token", "jti", jti, "provider", session.ProviderName, "error", err)
		}
		return ""
	}

	redirectURL, err := buildEndSessionURL(session.EndSessionEndpoint, idToken, h.postLogoutRedirectURL)
	if err != nil {
		auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "encrypt_error")...).Inc()
		(*auditDetail)["sso_logout"] = "invalid_endpoint"
		return ""
	}
	auth.SSOLogoutsTotal.WithLabelValues(observability.MetricValues(session.ProviderName, "redirected")...).Inc()
	(*auditDetail)["sso_logout"] = "redirected"
	return redirectURL
}

// buildEndSessionURL constructs the RP-initiated logout URL for an
// OIDC IdP. Standalone for testability — the parameter encoding
// matters (some IdPs are strict about URL-encoding of the embedded
// id_token's '=' padding) and we want a focused unit test that
// doesn't have to stand up a Logout handler.
//
// Parameters per the OIDC RP-Initiated Logout 1.0 spec:
//
//   - id_token_hint           — the upstream id_token (required)
//   - post_logout_redirect_uri — where the IdP bounces back to after
//     tearing down its session (optional;
//     omitted when empty so a strict IdP
//     that doesn't have it registered
//     doesn't 400)
//   - state                   — opaque round-tripped value (best-
//     effort CSRF marker; the landing
//     handler doesn't validate it because
//     /logout-done has no privileged
//     action to gate)
func buildEndSessionURL(endpoint, idToken, postLogoutRedirectURI string) (string, error) {
	if endpoint == "" {
		return "", errEmptyEndSessionEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := parsed.Query()
	q.Set("id_token_hint", idToken)
	if postLogoutRedirectURI != "" {
		q.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	}
	if state, gerr := generateLogoutState(); gerr == nil {
		q.Set("state", state)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// errEmptyEndSessionEndpoint is returned by buildEndSessionURL when
// the IdP didn't advertise an end_session_endpoint. Carried as a
// package-level sentinel so tests can assert on the exact error
// without string-matching.
var errEmptyEndSessionEndpoint = &endSessionError{"end_session_endpoint is empty"}

type endSessionError struct{ msg string }

func (e *endSessionError) Error() string { return e.msg }

// generateLogoutState returns a 32-byte URL-safe random string for
// the state parameter of the end-session redirect. The same shape
// as the SSO Login state — see internal/auth/oauth.go for the
// rationale (CSRF marker, opaque to the IdP).
func generateLogoutState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LogoutDone handles GET /api/v1/auth/logout-done/.
//
// This is the post_logout_redirect_uri the SSO Logout flow sends the
// IdP to bounce back to. The endpoint is intentionally minimal: it
// sets a one-shot "logged_out" cookie so the SPA can render a
// confirmation page on the next nav, and redirects to the dashboard's
// login screen. Auth: PUBLIC — by the time the IdP redirects here
// the user has already passed the bearer-revocation step, and the
// frontend is what actually decides what to render.
func (h *AuthHandler) LogoutDone(w http.ResponseWriter, r *http.Request) {
	// Best-effort marker cookie the SPA reads to flash "you've been
	// signed out everywhere" on the login page. Short-lived and not
	// signed — there's no security boundary here, just a UX hint.
	http.SetCookie(w, &http.Cookie{
		Name:     "astro_logged_out",
		Value:    "1",
		Path:     "/",
		HttpOnly: false, // SPA-readable
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
	// Audit the landing so the SOC 2 retention bundle includes the
	// full SLO loop: redirect issued → IdP processed → user returned.
	recordAuditAs(r, h.audit, pgtype.UUID{}, "auth.sso_logout_completed", "user", "", "", nil)
	// 303 See Other so the bouncing browser switches to GET regardless
	// of the IdP's choice of redirect method. /dashboard/login is the
	// SPA's marketed entrypoint; if the SPA isn't served by this
	// process the operator can override this later (the path is fixed
	// here only because it has no chart-level knob today).
	http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
}

// bearerTokenFromRequest extracts the JWT from the Authorization header.
// Used by Logout to pull the caller's JTI for the deny list — the auth
// middleware doesn't currently propagate the JTI into the AuthenticatedUser
// struct so we re-parse here. Returns empty when no bearer is present.
func bearerTokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		if c, err := r.Cookie(auth.SessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
			return c.Value
		}
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// ChangePasswordRequest is the body for POST /api/v1/auth/change-password/.
// openapi:request-operation postAuthChangePassword
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword handles POST /api/v1/auth/change-password/.
//
// Verifies the caller's current password, hashes the new one with bcrypt, and
// persists it via UpdateUserPasswordHash. Requires the auth middleware to have
// populated the request context with the authenticated user.
