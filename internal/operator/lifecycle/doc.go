// Package lifecycle is the reconcile engine for the retained Kubernetes-native
// management API (Cluster, Project, AgentProfile in management.astronomer.io).
//
// Every controller is a [Pipeline]: an ordered set of named [Stage]s
// registered once at construction. A reconcile takes a per-object lock
// ([KeyedLock], keyed by namespace/name) so concurrent reconciles of the same
// object serialize while different objects proceed concurrently; it then runs
// the stages in order. Each stage reports its outcome to the log, to the
// astronomer_operator_stage_* Prometheus metrics, and to an optional
// [Observer]. A stage error stops the chain, records the stage's failure
// reason on the object's Ready condition, and is returned so controller-runtime
// requeues the request with backoff.
//
// The manager, scheme and controller registration live in
// internal/operator/wire; this package has no knowledge of the manager.
package lifecycle
