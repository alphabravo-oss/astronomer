// Vault resolver observer adapter (migration 067).
//
// The vault.Resolver calls hooks on its Observer for every reference
// it resolves or fails. This file wires those hooks to the prometheus
// metrics defined in internal/observability/vault_metrics.go. We don't
// log the secret value here (per the no-secret-in-audit guard) — only
// the connection name + reference path + outcome.

package server

import (
	"context"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
)

// vaultMetricsObserver implements vault.Observer by forwarding to the
// prometheus counters / histograms in the observability package. It has
// no state; the metric vectors live as package-level globals.
type vaultMetricsObserver struct{}

func (vaultMetricsObserver) OnResolved(_ context.Context, connection string, _ vault.Reference, duration time.Duration) {
	observability.RecordVaultResolve(connection, observability.VaultOutcomeSucceeded, duration.Seconds())
}

func (vaultMetricsObserver) OnFailed(_ context.Context, connection string, _ vault.Reference, _ error) {
	// IMPORTANT: do not include the err or the reference's stored value
	// in any metric label — error strings can leak secret content if a
	// downstream library logged the value verbatim. Only outcome.
	observability.RecordVaultResolve(connection, observability.VaultOutcomeFailed, 0)
}

func (vaultMetricsObserver) OnHealth(_ context.Context, connection string, ok bool) {
	observability.RecordVaultHealth(connection, ok)
}
