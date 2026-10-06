package delivery

import (
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSourceMetricsExposeSampleAndPreservedObservationTimes(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	sourceTime := now.Add(-90 * time.Second)
	labels := observability.MetricValues("controllers")
	recordSource("controllers", protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &sourceTime}, now)
	if got := testutil.ToFloat64(observationSampledAt.WithLabelValues(labels...)); got != float64(now.Unix()) {
		t.Fatal("sample timestamp", got)
	}
	if got := testutil.ToFloat64(observationObservedAt.WithLabelValues(labels...)); got != float64(sourceTime.Unix()) {
		t.Fatal("observation timestamp restamped", got)
	}
	later := now.Add(30 * time.Second)
	recordSource("controllers", protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &sourceTime}, later)
	if testutil.ToFloat64(observationObservedAt.WithLabelValues(labels...)) != float64(sourceTime.Unix()) {
		t.Fatal("repeated sample renewed observation")
	}
	if testutil.ToFloat64(observationSampledAt.WithLabelValues(labels...)) != float64(later.Unix()) {
		t.Fatal("repeated sample did not update producer liveness")
	}
	recordSource("controllers", protocol.DeliveryObservation{State: protocol.ObservationUnsynced}, later)
	if testutil.ToFloat64(observationObservedAt.WithLabelValues(labels...)) != 0 || testutil.ToFloat64(observationSourceAge.WithLabelValues(labels...)) != -1 || testutil.ToFloat64(observationSourceAvailable.WithLabelValues(labels...)) != 0 {
		t.Fatal("unknown source not explicitly unavailable")
	}
}
