package handler

// Parallel helm dispatch.
//
// Catalog, Tool, and Monitoring reconcilers each pop a batch of pending
// operations and call helm.Do, which can block up to helmTimeout (10
// minutes). Before this change the reconciler held a per-handler mutex
// while iterating the batch serially, so a single stuck install could
// stall *all other clusters'* operations for up to 200 minutes.
//
// The fix: release the mutex after the claim phase (ListPending +
// MarkRunning) and dispatch executeOperation() calls via a bounded
// semaphore. Same-target double-dispatch is still prevented by:
//   - latestByTarget supersession within a single reconciler tick
//   - row-level "running" state on the operation table, so the next
//     tick's MarkRunning skips already-claimed rows
const defaultHelmDispatchConcurrency = 4

// effectiveHelmConcurrency normalizes a struct-field knob to a positive
// dispatch fan-out. Zero falls back to defaultHelmDispatchConcurrency
// (4); tests set the field directly to override.
func effectiveHelmConcurrency(n int) int {
	if n <= 0 {
		return defaultHelmDispatchConcurrency
	}
	return n
}
