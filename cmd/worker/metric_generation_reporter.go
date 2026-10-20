package main

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// The worker owns only SIEM callbacks. This reporter is outside the dispatch
// hot path; its stop function cancels and joins the single owned goroutine.
func startMetricGenerationReporter(ctx context.Context, begin observability.MetricGenerationBegin) func() {
	owned, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		observability.RunMetricGenerationCollector(owned, begin, []observability.MetricOwnerFamily{observability.MetricSIEMForwarder})
	}()
	return func() { cancel(); <-done }
}
