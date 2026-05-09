// Package agent: project_reconciler is reserved for the future agent-side
// drift watcher contemplated in the Phase B3 plan.
//
// The current B3 implementation runs entirely server-side: the worker task
// in internal/worker/tasks/project_reconcile.go uses the existing tunnel
// K8sRequester to apply ResourceQuota / LimitRange / NetworkPolicy and a
// 5-minute periodic sweep handles drift. That fits in the public protocol
// surface (no new MessageType) and ships now.
//
// The next iteration will move drift detection closer to the source by
// running a SharedInformerFactory inside the agent and emitting a
// "delete-of-our-managed-CR" event back to the server, which then enqueues
// a project:reconcile task with a single-namespace payload — much faster
// than waiting for the periodic sweep, and cheap because the informer is
// already paid for by Phase B6.
//
// Until B6 lands, this file exists only as the documented hook point for
// that follow-up work.
package agent

// ProjectReconcilerHook is a placeholder type so callers can wire the future
// informer-driven drift detector without another import-cycle dance. It does
// nothing today; B6 will replace the body once the SharedInformerFactory and
// STATE_UPDATE flow are landed.
type ProjectReconcilerHook struct{}

// NewProjectReconcilerHook returns a no-op hook. Callers (none today) can
// hold this in their agent state struct so the future wiring lands as a
// pure addition.
func NewProjectReconcilerHook() *ProjectReconcilerHook { return &ProjectReconcilerHook{} }
