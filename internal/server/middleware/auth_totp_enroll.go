package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

const enrollOnlyContextKey authContextKey = "totp_enroll_only_auth"

// IsTOTPEnrollOnlyAuth reports whether the current request authenticated via a
// PurposeTOTPEnrollOnly challenge (issued by Login when MFA enrollment is
// enforced and the user has not yet enrolled) rather than a full session. The
// enroll-confirm handler uses this to mint a real session once enrollment
// succeeds.
func IsTOTPEnrollOnlyAuth(ctx context.Context) bool {
	v, _ := ctx.Value(enrollOnlyContextKey).(bool)
	return v
}

// SetTOTPEnrollOnlyAuthForTest injects the enroll-only marker for tests.
func SetTOTPEnrollOnlyAuthForTest(ctx context.Context) context.Context {
	return context.WithValue(ctx, enrollOnlyContextKey, true)
}

// AuthOrTOTPEnrollChallenge authenticates a request via either a normal session
// (delegating to AuthWithQueries) OR a PurposeTOTPEnrollOnly challenge token.
//
// It exists to break the enrollment-enforcement lockout: when MFA enrollment is
// required, Login hands an unenrolled user a PurposeTOTPEnrollOnly token instead
// of a session, but every enrollment endpoint sat behind RequireAuth, which
// rejects all purpose tokens — so the user could never enroll and was locked out
// permanently. Mount ONLY the TOTP enroll start/confirm routes behind this so
// the enroll-only challenge is accepted there (and nowhere else).
func AuthOrTOTPEnrollChallenge(jwtManager *auth.JWTManager, queries TokenUserQuerier) func(http.Handler) http.Handler {
	normal := AuthWithQueries(jwtManager, queries)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if jwtManager != nil && queries != nil {
				if token, ok := bearerFromHeader(r); ok {
					if claims, err := jwtManager.ValidateToken(token); err == nil &&
						claims.TokenType == auth.PurposeToken &&
						claims.Purpose == auth.PurposeTOTPEnrollOnly {
						// The challenge is valid — resolve the user and fail
						// closed if the account was deleted or deactivated
						// between login and this request.
						dbUser, uerr := queries.GetUserByID(r.Context(), claims.UserID)
						if uerr != nil || !dbUser.IsActive {
							authError(w, "authentication_required", "Invalid or expired token")
							return
						}
						user := &AuthenticatedUser{
							ID:         dbUser.ID.String(),
							Email:      dbUser.Email,
							Username:   dbUser.Username,
							AuthMethod: "totp_enroll",
						}
						ctx := context.WithValue(r.Context(), userContextKey, user)
						ctx = context.WithValue(ctx, enrollOnlyContextKey, true)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
			// Not an enroll-only challenge (session JWT, API token, cookie, or
			// no/garbage token) → the standard auth path handles + rejects it.
			normal(next).ServeHTTP(w, r)
		})
	}
}

// bearerFromHeader extracts a Bearer token from the Authorization header only
// (never the session cookie — enroll challenges are carried by the SPA, not set
// as cookies).
func bearerFromHeader(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	return tok, tok != ""
}
