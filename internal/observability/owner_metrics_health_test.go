package observability

import (
	"testing"
	"time"
)

func TestOwnerMetricHealthDistinguishesIncompleteWithoutRefreshingTimestamp(t *testing.T) {
	baselineMetricInstance(t)
	initial := time.Unix(100, 0)
	RecordVaultMetricComplete(initial)
	for _, result := range []string{"owner_budget", "invalid", "canceled", "unrecognized-error-text"} {
		RecordVaultMetricFailure(result)
	}
	if got := vaultMetricCount(t, "astronomer_vault_metric_last_complete_timestamp_seconds", "", ""); got != 100 {
		t.Fatalf("failed Vault observation refreshed timestamp=%v", got)
	}
	labels := baselineMetricLabels(t, "astronomer_vault_metric_observations_total")
	if len(labels) != 5 {
		t.Fatalf("Vault observation health labels=%v", labels)
	}
	for _, label := range labels {
		switch label["result"] {
		case "complete", "owner_budget", "invalid", "canceled", "failed":
		default:
			t.Fatalf("unbounded result=%v", label)
		}
	}
	PublishRegistrationMetricSnapshot(nil, RegistrationMetricsWatermark(), initial)
	RecordRegistrationMetricFailure("canceled")
	RecordRegistrationMetricFailure("raw-synthetic-error")
	if got := vaultMetricCount(t, "astronomer_registration_metric_last_complete_timestamp_seconds", "", ""); got != 100 {
		t.Fatal("failed phase observation refreshed timestamp")
	}
	for _, label := range baselineMetricLabels(t, "astronomer_registration_metric_observations_total") {
		switch label["result"] {
		case "complete", "owner_budget", "invalid", "stale", "canceled", "failed":
		default:
			t.Fatalf("unbounded phase health result=%v", label)
		}
	}
}
