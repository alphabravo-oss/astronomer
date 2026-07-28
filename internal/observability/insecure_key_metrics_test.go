package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestReportInsecureDevKeys is the dev-keys-default-and-silent regression: a
// boot on a published key must be visible as both an ERROR log and a gauge, in
// every environment, and the gauge must go back to 0 once the key is replaced.
func TestReportInsecureDevKeys(t *testing.T) {
	oldInstanceID := InstanceID()
	SetInstanceID("test-insecure-keys")
	t.Cleanup(func() { SetInstanceID(oldInstanceID) })

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	ReportInsecureDevKeys(log, []string{"secret_key", "encryption_key"})
	for _, key := range []string{"secret_key", "encryption_key"} {
		if got := insecureDevKeyGauge(t, key); got != 1 {
			t.Fatalf("gauge for %q = %v, want 1", key, got)
		}
	}
	logged := buf.String()
	for _, want := range []string{"secret_key", "encryption_key", "insecure development key in use", "level=ERROR"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("log missing %q:\n%s", want, logged)
		}
	}

	// One key fixed: the other must publish a 0 rather than go stale.
	buf.Reset()
	ReportInsecureDevKeys(log, []string{"secret_key"})
	if got := insecureDevKeyGauge(t, "encryption_key"); got != 0 {
		t.Fatalf("gauge for a replaced key = %v, want 0", got)
	}
	if got := insecureDevKeyGauge(t, "secret_key"); got != 1 {
		t.Fatalf("gauge for the remaining sentinel = %v, want 1", got)
	}

	// Clean install: gauges cleared, nothing logged.
	buf.Reset()
	ReportInsecureDevKeys(log, nil)
	for _, key := range []string{"secret_key", "encryption_key"} {
		if got := insecureDevKeyGauge(t, key); got != 0 {
			t.Fatalf("gauge for %q on a clean install = %v, want 0", key, got)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("clean install logged %q, want silence", buf.String())
	}
}

func insecureDevKeyGauge(t *testing.T, key string) float64 {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	want := map[string]string{"astronomer_instance_id": InstanceID(), "key": key}
	for _, family := range families {
		if family.GetName() != "astronomer_insecure_dev_key_in_use" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if dropLabelsMatch(metric.GetLabel(), want) && metric.Gauge != nil {
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("no astronomer_insecure_dev_key_in_use sample for key=%q", key)
	return 0
}
