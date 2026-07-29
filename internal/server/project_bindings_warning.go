package server

import (
	"context"
	"log/slog"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// projectBindingLister is the one query warnInertProjectBindings needs, kept as
// an interface so the warning can be tested without a Postgres.
type projectBindingLister interface {
	ListProjectRoleBindings(ctx context.Context, arg sqlc.ListProjectRoleBindingsParams) ([]sqlc.ProjectRoleBinding, error)
}

// warnInertProjectBindings logs once at startup when project role bindings
// exist while namespace-scoped RBAC is disabled.
//
// Be precise about what the flag does and does not gate, because this line is
// the only operator-facing signal on the opt-out path:
//
//   - NOT gated. A project binding always resolves to the (cluster, namespace)
//     pairs its project owns (expandProjectBindings runs unconditionally), and
//     requireK8sProxyPermission's primary check (routes.go, engine.CheckPermission)
//     is not flag-guarded. So with the flag OFF a project member still reaches
//     namespace-EXPLICIT paths — /clusters/{c}/k8s/api/v1/namespaces/<ns>/pods,
//     /clusters/{c}/workloads/{kind}/{ns}/{name}/, and the rest.
//   - Gated. The cluster-wide LIST/WATCH allow-through-and-filter branch, the
//     namespace-scoped list gate on /clusters/{c}/workloads|pods|namespaces|events,
//     the per-namespace filtering of those list handlers, and kubectl-shell
//     caller scoping. Turning the flag off 403s a namespace-restricted caller
//     off those cluster-wide routes instead of serving them a filtered page.
//
// One row is enough to answer "do any exist", so this is a LIMIT 1 read, not a
// count. Any error is swallowed: a warning must never keep the server from
// starting.
func warnInertProjectBindings(ctx context.Context, q projectBindingLister, namespaceScopedRBAC bool, logger *slog.Logger) {
	if namespaceScopedRBAC || q == nil || logger == nil {
		return
	}
	rows, err := q.ListProjectRoleBindings(ctx, sqlc.ListProjectRoleBindingsParams{Limit: 1, Offset: 0})
	if err != nil || len(rows) == 0 {
		return
	}
	logger.Warn("project role bindings exist but namespace-scoped RBAC is disabled: " +
		"project members can still reach namespace-explicit paths (the project→namespace expansion is not gated on this flag), " +
		"but they are 403'd off cluster-wide list/watch routes (workloads, pods, namespaces, events) instead of getting a filtered page, " +
		"and kubectl-shell sessions are not scoped to the caller's grants. " +
		"Remove NAMESPACE_SCOPED_RBAC_ENABLED=false to restore the filtered behavior")
}
