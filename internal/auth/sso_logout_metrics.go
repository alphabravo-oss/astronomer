package auth

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

type ssoMetricIdentityReader interface {
	observability.MetricGenerationReader
	GetSSOProviderMetricIdentity(context.Context, string) (sqlc.GetSSOProviderMetricIdentityRow, error)
}
type SSOLogoutMetric struct {
	lease observability.MetricGenerationLease
}

func init() {
	observability.RegisterMetricGenerationRetirement(observability.MetricSSOProvider, func(instance, id string) {
		SSOLogoutsTotal.DeletePartialMatch(prometheus.Labels{"astronomer_instance_id": instance, "provider": id})
	})
}

// Historical provider names are preserved in audit/session behavior, but only a
// verified current configuration ID owns provider-specific telemetry.
func CaptureSSOLogoutMetric(ctx context.Context, source any, provider string) SSOLogoutMetric {
	reader, ok := source.(ssoMetricIdentityReader)
	if ctx == nil || ctx.Err() != nil || !ok || provider == "" {
		return SSOLogoutMetric{}
	}
	lookup := observability.BeginMetricGenerationLookup(observability.MetricSSOProvider)
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	row, err := reader.GetSSOProviderMetricIdentity(bounded, provider)
	if err != nil || bounded.Err() != nil {
		return SSOLogoutMetric{}
	}
	return SSOLogoutMetric{lookup.Bind(reader, observability.MetricGenerationRow{ID: row.ID, Generation: row.Generation})}
}

func (metric SSOLogoutMetric) Record(ctx context.Context, outcome string) {
	switch outcome {
	case "redirected", "no_endpoint", "no_session", "encrypt_error", "backchannel_ok", "backchannel_failed":
	default:
		outcome = "other"
	}
	if metric.lease.Record(ctx, func(instance, id string) { SSOLogoutsTotal.WithLabelValues(instance, id, outcome).Inc() }) {
		return
	}
	// An unverified or retired provider never allocates its supplied name. This
	// finite unknown bucket preserves attempts without implying a current owner.
	if ctx != nil && ctx.Err() == nil {
		SSOLogoutsTotal.WithLabelValues(observability.MetricValues("unknown", outcome)...).Inc()
	}
}
