package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricLabelsAreBoundedAndNeverRetainCallerInput(t *testing.T) {
	forbidden := "https://user:secret@example.test/tenant/cluster-123"
	ObserveSourceResolution(forbidden, forbidden, forbidden, time.Second)
	ObservePlacement(forbidden, time.Second, 1, []string{forbidden})
	ObserveRollout(forbidden, forbidden, forbidden)
	ObserveCohort(forbidden, forbidden, time.Second)
	ObserveDeployment(forbidden, true)
	ObserveSnapshot(forbidden, 1, 1)
	ObserveProtocol(forbidden, forbidden)
	SetFluxReadiness(forbidden, true, 1)
	ObserveStatus(forbidden)
	ObserveCredential(forbidden, forbidden)
	ObserveWorker(forbidden, forbidden)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	unknownSeen := false
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "astronomer_delivery_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, pair := range metric.Label {
				if strings.Contains(pair.GetValue(), forbidden) || strings.Contains(pair.GetValue(), "secret") || strings.Contains(pair.GetValue(), "cluster-123") {
					t.Fatalf("metric %s exposed unbounded caller input in %s=%q", family.GetName(), pair.GetName(), pair.GetValue())
				}
				if pair.GetValue() == "unknown" {
					unknownSeen = true
				}
			}
		}
	}
	if !unknownSeen {
		t.Fatal("invalid label values were not normalized to unknown")
	}
}
