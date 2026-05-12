package auth

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Auth hardening counters. Registered lazily so multiple test instances
// don't double-register and panic.
var (
	authMetricsOnce sync.Once

	// AccountLockoutsTotal counts how many times Login flipped a row
	// into a locked state.
	AccountLockoutsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_lockouts_total",
			Help:      "Total number of accounts that hit the failed-login lockout threshold.",
		},
		observability.MetricLabels("reason"),
	)

	// SessionRevocationsTotal counts JWT revocations — both user-driven
	// (logout) and admin-driven (force_logout). The `kind` label
	// distinguishes per-JTI from per-user invalidation.
	SessionRevocationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_revocations_total",
			Help:      "Total number of JWT revocations recorded (per-JTI or per-user invalidation).",
		},
		observability.MetricLabels("kind", "reason"),
	)

	// APITokenDeniedTotal counts API-token-authenticated requests
	// rejected by the hardening checks. The `reason` label is one of
	// {"scope","ip"}.
	APITokenDeniedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_api_token_denied_total",
			Help:      "Total API-token requests denied by scope or IP-allowlist enforcement.",
		},
		observability.MetricLabels("reason"),
	)

	// TOTPVerifiesTotal partitions the TOTP verify outcomes for the
	// 2FA dashboard. `outcome` is one of: "success", "failed",
	// "recovery" (recovery code consumed in place of a TOTP code).
	TOTPVerifiesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_totp_verifies_total",
			Help:      "Total number of TOTP / recovery-code verify attempts, by outcome.",
		},
		observability.MetricLabels("outcome"),
	)

	// TOTPEnrollmentsGauge tracks how many users currently have a
	// confirmed TOTP enrollment.
	TOTPEnrollmentsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "auth_totp_enrollments",
			Help:      "Number of users with a confirmed TOTP enrollment row.",
		},
	)
)

// RegisterAuthMetrics is idempotent; tests that spin up multiple
// JWTManager / handler instances would otherwise panic on the second
// Register call.
func RegisterAuthMetrics() {
	authMetricsOnce.Do(func() {
		for _, c := range []prometheus.Collector{
			AccountLockoutsTotal,
			SessionRevocationsTotal,
			APITokenDeniedTotal,
			TOTPVerifiesTotal,
			TOTPEnrollmentsGauge,
		} {
			if err := prometheus.Register(c); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
					panic(err)
				}
			}
		}
	})
}

func init() {
	RegisterAuthMetrics()
}
