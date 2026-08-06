// Package downstreamboundary provides content-free, process-local counters at
// the management-plane boundaries that can reach a downstream cluster agent.
// It deliberately records only fixed enums: never cluster IDs, paths, methods,
// payloads, users, or errors.
package downstreamboundary

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

type Entrypoint uint8

const (
	EntrypointKubernetesProxy Entrypoint = iota
	EntrypointTunnelMessage
	EntrypointTunnelBroadcast
	EntrypointRemoteDialer
	entrypointCount
)

type Operation uint8

const (
	OperationKubernetes Operation = iota
	OperationHelm
	OperationExec
	OperationLogs
	OperationServiceProxy
	OperationRBAC
	OperationAgentCommand
	operationCount
)

var counters [entrypointCount][operationCount]atomic.Uint64
var charlieCounters [entrypointCount][operationCount]atomic.Uint64

var boundaryCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "astronomer_downstream_boundary_calls_total",
	Help: "Management-plane attempts to cross a downstream cluster-agent boundary using fixed content-free labels.",
}, []string{"entrypoint", "operation"})

var charlieBoundaryCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "astronomer_charlie_downstream_boundary_calls_total",
	Help: "Charlie-originated attempts to cross a downstream cluster-agent boundary using fixed content-free labels.",
}, []string{"entrypoint", "operation"})

type charlieOriginKey struct{}

func init() {
	if err := prometheus.Register(boundaryCalls); err != nil {
		if registered, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, valid := registered.ExistingCollector.(*prometheus.CounterVec); valid {
				boundaryCalls = existing
			} else {
				panic(err)
			}
		} else {
			panic(err)
		}
	}
	if err := prometheus.Register(charlieBoundaryCalls); err != nil {
		if registered, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, valid := registered.ExistingCollector.(*prometheus.CounterVec); valid {
				charlieBoundaryCalls = existing
			} else {
				panic(err)
			}
		} else {
			panic(err)
		}
	}
	// Materialize every fixed combination so a zero-valued family is present
	// before the first call. Qualification must never confuse an absent probe
	// with proof that Charlie made no downstream attempt.
	for entrypoint := Entrypoint(0); entrypoint < entrypointCount; entrypoint++ {
		for operation := Operation(0); operation < operationCount; operation++ {
			boundaryCalls.WithLabelValues(entrypoint.String(), operation.String())
			charlieBoundaryCalls.WithLabelValues(entrypoint.String(), operation.String())
		}
	}
}

// WithCharlieOrigin marks a trusted in-process Charlie execution context. The
// marker is intentionally an unexported key rather than a request header: user,
// model, finding, or tool content cannot forge it.
func WithCharlieOrigin(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, charlieOriginKey{}, true)
}

// MarkCharlieOrigin is applied only to Astronomer's authenticated Charlie API
// route group. It does not inspect or trust any caller-controlled header.
func MarkCharlieOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithCharlieOrigin(r.Context())))
	})
}

// Record increments one bounded counter. Invalid values are ignored so an
// untrusted message type can never create an unbounded label or panic.
func Record(entrypoint Entrypoint, operation Operation) {
	record(context.Background(), entrypoint, operation)
}

// RecordContext records both the fleet-wide boundary attempt and, when the
// trusted context marker is present, the Charlie-attributed attempt. Callers at
// context-aware boundary entrypoints must use this form.
func RecordContext(ctx context.Context, entrypoint Entrypoint, operation Operation) {
	record(ctx, entrypoint, operation)
}

func record(ctx context.Context, entrypoint Entrypoint, operation Operation) {
	if entrypoint >= entrypointCount || operation >= operationCount {
		return
	}
	counters[entrypoint][operation].Add(1)
	boundaryCalls.WithLabelValues(entrypoint.String(), operation.String()).Inc()
	if ctx != nil && ctx.Value(charlieOriginKey{}) == true {
		charlieCounters[entrypoint][operation].Add(1)
		charlieBoundaryCalls.WithLabelValues(entrypoint.String(), operation.String()).Inc()
	}
}

type Snapshot struct {
	values [entrypointCount][operationCount]uint64
}

type Observation struct {
	Entrypoint string
	Operation  string
	Count      uint64
}

func TakeSnapshot() Snapshot {
	var snapshot Snapshot
	for entrypoint := Entrypoint(0); entrypoint < entrypointCount; entrypoint++ {
		for operation := Operation(0); operation < operationCount; operation++ {
			snapshot.values[entrypoint][operation] = counters[entrypoint][operation].Load()
		}
	}
	return snapshot
}

// TakeCharlieSnapshot returns only calls carrying the trusted Charlie origin.
// It is independent of normal downstream-agent fleet traffic.
func TakeCharlieSnapshot() Snapshot {
	var snapshot Snapshot
	for entrypoint := Entrypoint(0); entrypoint < entrypointCount; entrypoint++ {
		for operation := Operation(0); operation < operationCount; operation++ {
			snapshot.values[entrypoint][operation] = charlieCounters[entrypoint][operation].Load()
		}
	}
	return snapshot
}

func (after Snapshot) DeltaTotal(before Snapshot) uint64 {
	var total uint64
	for _, observation := range after.Delta(before) {
		total += observation.Count
	}
	return total
}

func (after Snapshot) Delta(before Snapshot) []Observation {
	changes := make([]Observation, 0)
	for entrypoint := Entrypoint(0); entrypoint < entrypointCount; entrypoint++ {
		for operation := Operation(0); operation < operationCount; operation++ {
			current, previous := after.values[entrypoint][operation], before.values[entrypoint][operation]
			if current > previous {
				changes = append(changes, Observation{Entrypoint: entrypoint.String(), Operation: operation.String(), Count: current - previous})
			}
		}
	}
	return changes
}

func KnownEntrypoints() []Entrypoint {
	return []Entrypoint{EntrypointKubernetesProxy, EntrypointTunnelMessage, EntrypointTunnelBroadcast, EntrypointRemoteDialer}
}

func KnownOperations() []Operation {
	return []Operation{OperationKubernetes, OperationHelm, OperationExec, OperationLogs, OperationServiceProxy, OperationRBAC, OperationAgentCommand}
}

func (entrypoint Entrypoint) String() string {
	switch entrypoint {
	case EntrypointKubernetesProxy:
		return "kubernetes_proxy"
	case EntrypointTunnelMessage:
		return "tunnel_message"
	case EntrypointTunnelBroadcast:
		return "tunnel_broadcast"
	case EntrypointRemoteDialer:
		return "remote_dialer"
	default:
		return "unknown"
	}
}

func (operation Operation) String() string {
	switch operation {
	case OperationKubernetes:
		return "kubernetes"
	case OperationHelm:
		return "helm"
	case OperationExec:
		return "exec"
	case OperationLogs:
		return "logs"
	case OperationServiceProxy:
		return "service_proxy"
	case OperationRBAC:
		return "rbac"
	case OperationAgentCommand:
		return "agent_command"
	default:
		return "unknown"
	}
}
