// Package tasks — migration 065 / sprint 17 kubectl session reaper.
//
// Periodic task ("kubectl:session_reap") scheduled every 60 seconds. On
// each fire:
//
//  1. Idle/hard-cap sweep — rows where last_input_at + idle_timeout <
//     now() OR expires_at < now() are flipped to status='expired' and
//     the per-row Pod / ClusterRoleBinding / ClusterRole /
//     ServiceAccount tuple is torn down via the cluster's tunnel
//     K8sRequester.
//
//  2. Orphan pod sweep — for every cluster with active sessions, list
//     pods labelled astronomer.io/component=kubectl-shell in
//     kube-system, and delete any whose name doesn't match an active
//     row. Belt-and-braces against a Close() crash that left a pod
//     around without a DB row.
//
// The reaper runs from the asynq worker pool and is safe to invoke
// concurrently with active sessions: it never modifies a row that is
// still inside its idle/hard-cap window, and tear-down is idempotent
// (DELETE returning 404 is treated as success).
package tasks

import (
	"context"

	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
)

// KubectlSessionReapType is the asynq task identifier scheduler entries
// reference. Public so the worker scheduler can wire the cadence.
const KubectlSessionReapType = "kubectl:session_reap"

// NewKubectlSessionReapTask builds the asynq envelope for one reaper
// fire. Empty payload — the runtime deps carry everything.
func NewKubectlSessionReapTask() *asynq.Task {
	return asynq.NewTask(KubectlSessionReapType, nil, asynq.MaxRetry(2))
}

// KubectlSessionReapDeps is the wiring for the reaper. Built once at
// server startup and passed to Configure; tests can swap fakes in.
type KubectlSessionReapDeps struct {
	Deps kubectl.Deps
}

var kubectlReapDeps KubectlSessionReapDeps

// ConfigureKubectlSessionReap stores the reaper's runtime deps.
func ConfigureKubectlSessionReap(deps KubectlSessionReapDeps) {
	kubectlReapDeps = deps
}

// ResetKubectlSessionReap clears runtime deps. Tests use this.
func ResetKubectlSessionReap() {
	kubectlReapDeps = KubectlSessionReapDeps{}
}

// HandleKubectlSessionReap is the asynq handler. The leader-election
// wrapper around it ensures only one replica fires per tick.
func HandleKubectlSessionReap(ctx context.Context, _ *asynq.Task) error {
	if kubectlReapDeps.Deps.Queries == nil {
		// Feature disabled or not yet wired — exit quietly so the
		// scheduler's idempotent re-enqueue doesn't generate alerts.
		return nil
	}
	return kubectl.Reap(ctx, kubectlReapDeps.Deps)
}

