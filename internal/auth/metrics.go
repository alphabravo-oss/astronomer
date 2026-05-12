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
	// into a locked state. Compliance reviewers want this as a
	// time-series so a brute-force attack is visible in dashboards.
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
	// distinguishes per-JTI (single token) from per-user (all tokens
	// via tokens_invalidated_at cutoff).
	SessionRevocationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "auth_revocations_total",
			Help:      "Total number of JWT revocations recorded (per-JTI or per-user invalidation).",
		},
		observability.MetricLabels("kind", "reason"),
	)

	// TOTPVerifiesTotal partitions the TOTP verify outcomes for the
	// 2FA dashboard. `outcome` is one of: "success" (regular TOTP
	// code accepted), "failed" (code/recovery did not match), or
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
	// confirmed TOTP enrollment. Refreshed by a periodic poller
	// (or on demand by the admin endpoint) — Prometheus pulls the
	// last-set value. Useful for compliance reporting + spotting
	// downward drift (force-disable storms).
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
