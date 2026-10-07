// Package kuberequests counts observable HTTP RoundTrip attempts made by the
// agent's instrumented shared Kubernetes client family. It does not count watch
// frames, TCP attempts, or retries internal to http.Transport.
package kuberequests

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/rest"
)

type Consumer uint8

const (
	Other Consumer = iota
	SharedObservation
	DeliveryInventory
	DeliveryAssignmentObservation
)

func (c Consumer) label() string {
	switch c {
	case SharedObservation:
		return "shared_observation"
	case DeliveryInventory:
		return "delivery_inventory"
	case DeliveryAssignmentObservation:
		return "delivery_assignment_observation"
	default:
		return "other"
	}
}

type consumerKey struct{}
type spanKey struct{}
type attemptSpan struct{ nested atomic.Bool }

var requests = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "astronomer", Subsystem: "agent", Name: "kubernetes_requests_total", Help: "Observable HTTP RoundTrip attempts from instrumented shared Kubernetes clients, excluding watch frames and retries internal to the underlying transport."}, observability.MetricLabels("consumer", "operation", "resource", "outcome"))
var instrumentation = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "astronomer", Subsystem: "agent", Name: "kubernetes_request_instrumentation_info", Help: "Installed instrumented transport-family schema; does not certify all-process Kubernetes client coverage."}, observability.MetricLabels("schema"))

func init() { prometheus.MustRegister(requests, instrumentation) }

// WithConsumer preserves cancellation and existing context values. Call at
// informer ListWatch callbacks, since factory.Start does not propagate values.
func WithConsumer(ctx context.Context, consumer Consumer) context.Context {
	return context.WithValue(ctx, consumerKey{}, consumer)
}

// Config preserves the caller's config and wrapper order. Installing the meter
// inside existing wrappers also counts each attempt made by a retrying wrapper.
// Repeated installation is safe: only the innermost meter records an attempt.
func Config(original *rest.Config) *rest.Config {
	cfg := rest.CopyConfig(original)
	previous := cfg.WrapTransport
	cfg.WrapTransport = nil
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		instrumentation.WithLabelValues(observability.MetricValues("v1")...).Set(1)
		return &meteredTransport{next: rt}
	})
	cfg.Wrap(previous)
	return cfg
}

// DefaultConsumerConfig tags context-less legacy discovery calls without
// overriding explicit context tags. It does not install another counter.
func DefaultConsumerConfig(original *rest.Config, consumer Consumer) *rest.Config {
	cfg := rest.CopyConfig(original)
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &defaultConsumerTransport{next: rt, consumer: consumer}
	})
	return cfg
}

type defaultConsumerTransport struct {
	next     http.RoundTripper
	consumer Consumer
}

func (t *defaultConsumerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, ok := r.Context().Value(consumerKey{}).(Consumer); !ok {
		r = r.WithContext(WithConsumer(r.Context(), t.consumer))
	}
	return t.next.RoundTrip(r)
}

type meteredTransport struct{ next http.RoundTripper }

func (t *meteredTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if parent, ok := r.Context().Value(spanKey{}).(*attemptSpan); ok {
		parent.nested.Store(true)
	}
	span := &attemptSpan{}
	request := r.WithContext(context.WithValue(r.Context(), spanKey{}, span))
	response, err := t.next.RoundTrip(request)
	if !span.nested.Load() {
		consumer, _ := r.Context().Value(consumerKey{}).(Consumer)
		operation, resource := classify(r)
		outcome := responseOutcome(response, err)
		requests.WithLabelValues(observability.MetricValues(consumer.label(), operation, resource, outcome)...).Inc()
	}
	return response, err
}
func responseOutcome(response *http.Response, err error) string {
	if err != nil {
		return "transport_error"
	}
	if response == nil {
		return "other"
	}
	switch response.StatusCode / 100 {
	case 1:
		return "1xx"
	case 2:
		return "2xx"
	case 3:
		return "3xx"
	case 4:
		return "4xx"
	case 5:
		return "5xx"
	default:
		return "other"
	}
}
