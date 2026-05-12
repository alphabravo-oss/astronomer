package auth

import "time"

// Account lockout policy (NIST 800-53 AC-7, ISO 27001 A.9.4.2, SOC 2 CC6.1).
//
// These are package-level defaults — the actual values used at runtime come
// from the loaded Config (chart-tunable). Anything that compares against
// these constants directly is fine to keep them as compile-time fallbacks.
const (
	// LoginFailureThreshold is the number of consecutive failed login
	// attempts before an account is locked. The counter resets on a
	// successful login or an admin unlock.
	LoginFailureThreshold = 5

	// LockoutDuration is how long an account stays locked after the
	// threshold is exceeded. Auto-unlock is implicit: the JWT/Login flow
	// just compares `locked_until` against `time.Now()`.
	LockoutDuration = 15 * time.Minute

	// LockoutReasonTooManyFailedAttempts is the canonical reason string
	// written into `users.locked_reason` when the Login handler trips the
	// lockout threshold. Surfaced to admins via /users/{id}/.
	LockoutReasonTooManyFailedAttempts = "too_many_failed_attempts"

	// LockoutReasonAdminLocked is set when an admin force-locks an
	// account via the admin endpoint (kept for future use; current
	// admin flow exposes UNLOCK only).
	LockoutReasonAdminLocked = "admin_locked"
)
