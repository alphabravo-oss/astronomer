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
)

// RegisterAuthMetrics is idempotent; tests that spin up multiple
// JWTManager / handler instances would otherwise panic on the second
// Register call.
func RegisterAuthMetrics() {
	authMetricsOnce.Do(func() {
		for _, c := range []prometheus.Collector{AccountLockoutsTotal, SessionRevocationsTotal} {
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
