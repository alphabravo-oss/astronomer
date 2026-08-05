// Package downstreamboundary provides content-free, process-local counters at
// the management-plane boundaries that can reach a downstream cluster agent.
// It deliberately records only fixed enums: never cluster IDs, paths, methods,
// payloads, users, or errors.
package downstreamboundary

import (
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

var boundaryCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "astronomer_downstream_boundary_calls_total",
	Help: "Management-plane attempts to cross a downstream cluster-agent boundary using fixed content-free labels.",
}, []string{"entrypoint", "operation"})

func init() {
	if err := prometheus.Register(boundaryCalls); err != nil {
		if registered, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, valid := registered.ExistingCollector.(*prometheus.CounterVec); valid {
				boundaryCalls = existing
				return
			}
		}
		panic(err)
	}
}

// Record increments one bounded counter. Invalid values are ignored so an
// untrusted message type can never create an unbounded label or panic.
func Record(entrypoint Entrypoint, operation Operation) {
	if entrypoint >= entrypointCount || operation >= operationCount {
		return
	}
	counters[entrypoint][operation].Add(1)
	boundaryCalls.WithLabelValues(entrypoint.String(), operation.String()).Inc()
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
