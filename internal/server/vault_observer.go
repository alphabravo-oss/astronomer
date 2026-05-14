// Vault resolver observer adapter (migration 067).
//
// The vault.Resolver calls hooks on its Observer for every reference
// it resolves or fails. This file wires those hooks to the prometheus
// metrics defined in internal/observability/vault_metrics.go AND
// (T6.067) writes per-resolution audit rows so the audit log can
// answer "which references did this install resolve, when?" without
// scraping prom history.
//
// IMPORTANT: we never log the resolved secret value — only the
// reference path + outcome. Same constraint that lives on the
// observer interface docstring.

package server

import (
	"context"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/vault"
)

// vaultMetricsObserver implements vault.Observer by forwarding to the
// prometheus counters / histograms in the observability package AND
// to the audit log. The audit writer is optional — nil-safe; the
// metrics path stays live even without an audit destination.
type vaultMetricsObserver struct {
	audit audit.Querier
}

func newVaultMetricsObserver(audit audit.Querier) vaultMetricsObserver {
	return vaultMetricsObserver{audit: audit}
}

func (o vaultMetricsObserver) OnResolved(ctx context.Context, connection string, ref vault.Reference, duration time.Duration) {
	observability.RecordVaultResolve(connection, observability.VaultOutcomeSucceeded, duration.Seconds())
	if o.audit == nil {
		return
	}
	audit.Record(ctx, o.audit, audit.Event{
		Source:       "server",
		Action:       "vault.reference.resolved",
		ResourceType: "vault_reference",
		ResourceID:   ref.Raw,
		Detail: map[string]any{
			"connection":  connection,
			"engine":      ref.Engine,
			"path":        ref.Path,
			"key":         ref.Key,
			"duration_ms": duration.Milliseconds(),
		},
	})
}

func (o vaultMetricsObserver) OnFailed(ctx context.Context, connection string, ref vault.Reference, err error) {
	observability.RecordVaultResolve(connection, observability.VaultOutcomeFailed, 0)
	if o.audit == nil {
		return
	}
	// Per the Observer docstring, do NOT pass err.Error() into the
	// audit detail map — downstream libraries (notably hashicorp/vault
	// api) can return error strings that quote the secret value. We
	// classify into a coarse reason field instead so the audit row is
	// informative without leaking content.
	audit.Record(ctx, o.audit, audit.Event{
		Source:       "server",
		Action:       "vault.reference.failed",
		ResourceType: "vault_reference",
		ResourceID:   ref.Raw,
		Detail: map[string]any{
			"connection": connection,
			"engine":     ref.Engine,
			"path":       ref.Path,
			"key":        ref.Key,
			"reason":     vaultErrorClass(err),
		},
	})
}

func (o vaultMetricsObserver) OnHealth(_ context.Context, connection string, ok bool) {
	observability.RecordVaultHealth(connection, ok)
}

// vaultErrorClass maps the raw error into a coarse, secret-free
// classification so the audit row carries useful triage information
// without quoting any error string verbatim.
func vaultErrorClass(err error) string {
	if err == nil {
		return "unknown"
	}
	s := err.Error()
	switch {
	case contains(s, "connection refused"), contains(s, "no such host"), contains(s, "i/o timeout"):
		return "connectivity"
	case contains(s, "403"), contains(s, "permission denied"), contains(s, "forbidden"):
		return "permission_denied"
	case contains(s, "404"), contains(s, "not found"):
		return "not_found"
	case contains(s, "401"), contains(s, "unauthorized"), contains(s, "invalid token"):
		return "unauthorized"
	default:
		return "other"
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
