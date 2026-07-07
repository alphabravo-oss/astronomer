// Package handler — kubectl-shell caller-scoping (opt-in).
//
// The v1 break-glass shell (kubectl_shell.go) provisions an in-cluster
// ServiceAccount whose ClusterRole mirrors a COARSE verb envelope
// (read / +write / +delete / cluster-admin) across every namespace.
// Opening the shell only proves the clusters:update RBAC gate at the
// route, so a caller who holds cluster:update — even one scoped to a
// single project/namespace by their astronomer bindings — receives a
// blanket, cluster-wide grant via the agent SA. That is the
// escalation this file closes.
//
// The scoping here is OPT-IN and DEFAULT-OFF: it only runs when the
// platform-settings flag `feature.shell_scope_to_caller` is set true.
// With the flag off (the default) every code path below is bypassed and
// the shell behaves exactly as before. See docs/kubectl-shell.md.
//
// When ON, the caller's own astronomer RoleBindings — not the agent SA —
// define an envelope:
//
//   - superuser ............... full cluster, verbs as requested.
//   - cluster/global binding .. cross-namespace ("-A") visibility,
//     write verbs only if the caller actually holds clusters:update /
//     :delete on this cluster.
//   - namespace-scoped only ... confined to those namespaces AND capped
//     at read-only, because the coarse v1 ClusterRole cannot express a
//     per-namespace write grant — handing a single-namespace operator a
//     cluster-wide create/update/patch role would re-introduce the very
//     escalation we are closing.
//   - nothing applicable ...... scope is UNDETERMINED and, with the flag
//     on, the shell FAILS CLOSED (the caller is denied). We never fall
//     back to the blanket SA grant.
//
// Enforcement points wired from kubectl_shell.go behind the flag:
//   - Open(): constrains kubectl.EffectiveVerbs to the derived envelope
//     and denies undetermined scopes.
//   - HandleWS(): re-derives + fails closed before the WS upgrade, and
//     tags out-of-scope namespace targets in the command audit trail.
//
// Header-based enforcement (K8s impersonation) is provided by
// CallerScope.ImpersonationHeaders() for the exec proxy to adopt; see
// integration notes.
package handler

import (
	"context"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/kubectl"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// shellScopeToCallerFlag is the platform_settings key that opts a
// deployment into caller-scoped kubectl shells. DEFAULT FALSE: the
// fallback passed to BoolValue is false, so absent an explicit
// operator opt-in the existing coarse behaviour is used unchanged.
const shellScopeToCallerFlag = "feature.shell_scope_to_caller"

// ShellFeatureReader is the minimal platform-settings surface the scope
// path needs. *handler.SettingsCache satisfies it (same shape as
// middleware.FeatureFlagReader). nil-safe at the call site: a nil
// reader means the flag is treated as OFF, i.e. existing behaviour.
type ShellFeatureReader interface {
	BoolValue(ctx context.Context, key string, fallback bool) bool
}

// CallerScope is the RBAC-derived envelope a caller-scoped kubectl
// shell is confined to. It is derived purely from the caller's
// astronomer RoleBindings against the target cluster — never from the
// agent ServiceAccount's blanket grant.
type CallerScope struct {
	// Determined is false when no applicable binding could be resolved
	// for the caller against this cluster. With the scope feature ON an
	// undetermined scope MUST fail closed (deny the shell); it must
	// NEVER fall back to the blanket SA grant.
	Determined bool
	// AllNamespaces is true when the caller holds a cluster-wide or
	// global binding that is not narrowed to a single namespace — such
	// callers keep cross-namespace ("kubectl get pods -A") visibility.
	AllNamespaces bool
	// Namespaces is the explicit set of Kubernetes namespaces the
	// caller's namespace-scoped bindings grant. Ignored when
	// AllNamespaces is true.
	Namespaces map[string]struct{}
	// Verbs is the coarse verb envelope, already intersected with what
	// the caller's bindings actually grant on this cluster and capped at
	// read-only for namespace-restricted callers.
	Verbs kubectl.EffectiveVerbs
	// Caller is the astronomer user id; surfaced as the K8s
	// impersonation identity for proxies that adopt header-based
	// enforcement.
	Caller uuid.UUID
	// Superuser records that the scope was granted via a superuser
	// binding (full cluster, verbs as requested).
	Superuser bool
}

// deriveCallerScope maps a caller's RoleBindings to a CallerScope for a
// given cluster. `requested` is the verb envelope the caller asked for
// (already gated by the H5 elevation opt-in in effectiveVerbsFor); the
// derived scope can only ever narrow it.
//
// Fail-closed contract: if engine is nil, or no binding applies to the
// cluster, Determined stays false and callers under the flag must deny.
func deriveCallerScope(engine *rbac.Engine, bindings []rbac.RoleBinding, clusterID, callerID uuid.UUID, requested kubectl.EffectiveVerbs) CallerScope {
	s := CallerScope{
		Namespaces: map[string]struct{}{},
		Caller:     callerID,
	}
	if engine == nil {
		return s
	}
	// Superuser: full cluster, verbs as requested. A superuser shell is
	// itself a deliberate elevation gated upstream (effectiveVerbsFor).
	if engine.CheckSuperuser(bindings) {
		s.Determined = true
		s.AllNamespaces = true
		s.Superuser = true
		s.Verbs = requested
		return s
	}
	for _, b := range bindings {
		if !bindingAppliesToCluster(b, clusterID) {
			continue
		}
		// A RAW project binding (project-scoped, no explicit cluster or
		// namespace) is NOT a cross-namespace grant — treating it as
		// AllNamespaces was the DIR-04 over-grant that handed a
		// project-restricted operator the whole cluster. Its concrete
		// namespaces come from the project→namespace expansion (the
		// synthetic cluster+namespace bindings GetUserBindings emits when
		// namespace_scoped_rbac is enabled), folded in via
		// AuthorizedNamespaces below. Skip it here so it neither sets
		// AllNamespaces nor (on its own) determines the scope.
		if b.ProjectID != "" && b.ClusterID == "" {
			continue
		}
		s.Determined = true
		if b.Namespace != "" {
			// Namespace-scoped binding contributes exactly that namespace.
			s.Namespaces[b.Namespace] = struct{}{}
			continue
		}
		// A cluster-wide or global binding (no namespace narrowing)
		// grants cross-namespace scope.
		s.AllNamespaces = true
	}
	// Fold in the caller's REAL effective namespace grant on this cluster.
	// AuthorizedNamespaces resolves the project→namespace expansion (and any
	// native per-namespace list grants) into a concrete allow-set for the
	// resources a shell reads, replacing the old coarse "determined but
	// empty" project handling. It fails closed (all==false, empty set) when
	// nothing applies. A cluster-wide grant here promotes to AllNamespaces.
	if !s.AllNamespaces {
		if all, names := engine.AuthorizedNamespaces(bindings, rbac.ResourcePods, rbac.VerbList, clusterID); all {
			s.Determined = true
			s.AllNamespaces = true
		} else {
			for ns := range names {
				s.Determined = true
				s.Namespaces[ns] = struct{}{}
			}
		}
	}
	if !s.Determined {
		return s
	}
	// Verb envelope. Read is always granted (opening the shell proved
	// clusters:update at the route gate). Write/delete are granted only
	// for a cross-namespace caller who actually holds the matching
	// cluster verb — a namespace-restricted caller is capped at
	// read-only because the coarse v1 ClusterRole cannot narrow a write
	// grant to a namespace, and a cluster-wide write role would be an
	// escalation for a single-namespace operator.
	s.Verbs = kubectl.EffectiveVerbs{Read: true}
	if s.AllNamespaces {
		if requested.Update && engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbUpdate, clusterID, uuid.Nil) {
			s.Verbs.Update = true
		}
		if requested.Delete && engine.CheckPermission(bindings, rbac.ResourceClusters, rbac.VerbDelete, clusterID, uuid.Nil) {
			s.Verbs.Delete = true
		}
	}
	return s
}

// bindingAppliesToCluster reports whether a binding is relevant to the
// target cluster for scope-derivation purposes. Mirrors the spirit of
// rbac.Engine.bindingApplies (which is unexported). NOTE (DIR-04): a raw
// project-scoped binding (ProjectID set, no explicit cluster) is reported
// applicable here, but deriveCallerScope no longer treats it as a
// cross-namespace grant — it resolves the project's concrete namespaces via
// engine.AuthorizedNamespaces (which consumes the project→namespace
// synthetic-binding expansion). This function stays permissive so that
// path can run; the confinement happens in deriveCallerScope.
func bindingAppliesToCluster(b rbac.RoleBinding, clusterID uuid.UUID) bool {
	if b.IsSuperuser {
		return true
	}
	// Global binding (no cluster, no project): applies everywhere.
	if b.ClusterID == "" && b.ProjectID == "" {
		return true
	}
	if b.ClusterID != "" && b.ClusterID == clusterID.String() {
		return true
	}
	// Project-scoped bindings can grant access to this cluster's
	// namespaces; we can't resolve the project→cluster mapping here, so
	// we accept them as applicable but they only contribute a namespace
	// when one is explicitly set on the binding.
	if b.ProjectID != "" {
		return true
	}
	return false
}

// Allows reports whether a command that targets `namespace` is within
// the scope. An empty namespace ("current"/unspecified) is always
// allowed — the pod's own default context is bounded by the SA grant,
// not the flag. Cross-namespace ("-A") targeting is represented by the
// sentinel namespaceAllSentinel and is only allowed for AllNamespaces
// scopes.
func (s CallerScope) Allows(namespace string) bool {
	if s.AllNamespaces {
		return true
	}
	if namespace == "" {
		return true
	}
	if namespace == namespaceAllSentinel {
		// "-A" against a namespace-restricted scope is out of scope.
		return false
	}
	_, ok := s.Namespaces[namespace]
	return ok
}

// SortedNamespaces returns the explicit namespace grants in a stable
// order for audit records / logging.
func (s CallerScope) SortedNamespaces() []string {
	out := make([]string, 0, len(s.Namespaces))
	for ns := range s.Namespaces {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// ImpersonationHeaders returns the K8s impersonation headers a proxy
// can attach to per-request calls so the apiserver evaluates the
// caller's identity (RBAC) instead of the shell SA's blanket grant.
// Superuser scopes are NOT impersonated (they legitimately need the
// cluster-admin binding). Returns nil when there is nothing to
// impersonate. Consumed by the exec proxy if/when it adopts
// header-based enforcement — see integration notes.
func (s CallerScope) ImpersonationHeaders() map[string]string {
	if s.Superuser || s.Caller == uuid.Nil {
		return nil
	}
	return map[string]string{
		"Impersonate-User": "astronomer:user:" + s.Caller.String(),
	}
}

// scopeEnforcementLabel names the in-cluster enforcement shape a session
// gets, for the audit trail. "cluster-admin" and "cluster-role" are
// cross-namespace; "namespaced-role" is the confined mechanism-A path where
// per-namespace Roles genuinely bound the session ServiceAccount at the
// apiserver (not merely audited).
func scopeEnforcementLabel(s CallerScope) string {
	switch {
	case s.Superuser:
		return "cluster-admin"
	case s.AllNamespaces:
		return "cluster-role"
	default:
		return "namespaced-role"
	}
}

// namespaceAllSentinel represents a cross-namespace ("-A" /
// --all-namespaces) command target in scope checks.
const namespaceAllSentinel = "\x00__all_namespaces__"

// namespaceTargetsFromCommand best-effort extracts the namespace(s) a
// kubectl command line targets. It recognises the standard flag forms:
//
//	-n <ns> / -n=<ns>
//	--namespace <ns> / --namespace=<ns>
//	-A / --all-namespaces  → namespaceAllSentinel
//
// It is deliberately conservative: it returns the empty slice when no
// namespace flag is present (the command runs against the pod's default
// context) and never guesses. This is a DETECTIVE control feeding the
// audit trail — see the note in kubectl_shell.go's WS recorder about
// why hard per-keystroke blocking is not possible from the recorder.
func namespaceTargetsFromCommand(line string) []string {
	fields := strings.Fields(line)
	var out []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "-A", f == "--all-namespaces":
			out = append(out, namespaceAllSentinel)
		case f == "-n", f == "--namespace":
			if i+1 < len(fields) {
				out = append(out, fields[i+1])
				i++
			}
		case strings.HasPrefix(f, "--namespace="):
			out = append(out, strings.TrimPrefix(f, "--namespace="))
		case strings.HasPrefix(f, "-n="):
			out = append(out, strings.TrimPrefix(f, "-n="))
		}
	}
	return out
}

// shellScopeEnabled reports whether caller-scoping is active. Task 009
// (DIR-04) collapses the two multi-tenancy switches: scoping is ON when
// EITHER the master namespace_scoped_rbac_enabled config flag is set
// (h.NamespaceScopedRBAC, the promoted default) OR the legacy
// feature.shell_scope_to_caller platform setting is explicitly on. A nil
// reader and an unset key both yield false for the legacy path, so with
// namespace-scoped RBAC off the shell keeps its pre-feature behaviour.
func (h *KubectlShellHandler) shellScopeEnabled(ctx context.Context) bool {
	if h == nil {
		return false
	}
	if h.NamespaceScopedRBAC {
		return true
	}
	if h.Features == nil {
		return false
	}
	return h.Features.BoolValue(ctx, shellScopeToCallerFlag, false)
}

// deriveScopeForCaller looks up the caller's bindings and derives the
// CallerScope. The bool return is false when scope could not be
// determined — callers under the flag must fail closed on false.
func (h *KubectlShellHandler) deriveScopeForCaller(ctx context.Context, userID, clusterID uuid.UUID, requested kubectl.EffectiveVerbs) (CallerScope, bool) {
	if h.Bindings == nil || h.RBACEngine == nil {
		// Can't prove scope → undetermined → fail closed under the flag.
		return CallerScope{Namespaces: map[string]struct{}{}, Caller: userID}, false
	}
	bindings, err := h.Bindings.GetUserBindings(ctx, userID.String())
	if err != nil {
		return CallerScope{Namespaces: map[string]struct{}{}, Caller: userID}, false
	}
	scope := deriveCallerScope(h.RBACEngine, bindings, clusterID, userID, requested)
	return scope, scope.Determined
}

// SetFeatureFlags wires the platform-settings reader used to evaluate
// feature.shell_scope_to_caller. Optional — when unset the scope
// feature is OFF and the shell keeps its existing behaviour. Mirrors
// the other Set* wiring hooks so the constructor signature stays stable.
func (h *KubectlShellHandler) SetFeatureFlags(r ShellFeatureReader) {
	if h == nil {
		return
	}
	h.Features = r
}
