package handler

// Destination-scoped authorization for the ArgoCD lifecycle routes.
//
// Every route in argocd.go resolves `{id}` to an ArgoCD instance and gates on
// the cluster that instance RUNS ON (loadInstance). That is the wrong cluster:
// an Application deploys to its `spec.destination`, which is routinely a
// different, adopted cluster. Anchoring the check to the instance meant a
// principal holding workloads:update on the one cluster hosting Argo CD could
// sync-with-prune, patch or delete Applications delivering to every other
// tenant's cluster. The instance gate stays (it is the "may you talk to this
// Argo CD at all" check); the helpers here add the second, destination-scoped
// gate the operation actually needs.
//
// Fail-closed contract: a destination that does not resolve to a cluster this
// product knows about is DENIED, not allowed. Unresolvable covers an empty
// destination, an unregistered server URL, and a cluster name we never
// registered (e.g. a registration made with a `name` override, which upstream
// Argo CD knows but argocd_managed_clusters does not record).
//
// Unrestricted callers — superusers, and the machine paths where no
// authenticated user is on the context — short-circuit BEFORE resolution, so
// the reconciler and self-manage flows are untouched and a superuser can still
// operate on an Application whose destination was registered out-of-band.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// errArgoDestinationUnresolved marks a destination that maps to no cluster we
// know about. Callers translate it to 403 — never to a pass.
var errArgoDestinationUnresolved = errors.New("argocd destination does not resolve to a managed cluster")

// argoCDInClusterDestinations are the two ways Argo CD names its own cluster.
// Both mean "the cluster argocd-server runs in", which is exactly the instance
// row's cluster_id — so resolving them to instance.ClusterID is not a fallback
// to the old instance-anchored behaviour, it is the correct answer.
var argoCDInClusterDestinations = map[string]struct{}{
	"in-cluster":                      {},
	"https://kubernetes.default.svc":  {},
	"https://kubernetes.default.svc/": {},
}

// destinationAuthzBindings loads the caller's bindings once for a
// destination check. restricted==false means the caller is a superuser or the
// request carries no authenticated user (machine path); the caller should skip
// the check entirely rather than resolve a destination it will not use.
// ok==false means the response has already been written.
func (h *ArgoCDHandler) destinationAuthzBindings(w http.ResponseWriter, r *http.Request) (bindings []rbac.RoleBinding, restricted, ok bool) {
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return nil, false, false
	}
	// bindingsForContext reports restricted=true for EVERY authenticated user:
	// superuser is expressed as a RoleBinding the engine consumes, not as that
	// flag. Fold it in here, otherwise the short-circuit documented above is
	// not real and a superuser is denied outright on a destination that does
	// not resolve — a cluster added out of band with `argocd cluster add`, or a
	// registration made under a `name` override — with no escape hatch
	// anywhere in the product.
	if restricted && h.authz.engine != nil && h.authz.engine.CheckSuperuser(bindings) {
		restricted = false
	}
	return bindings, restricted, true
}

// resolveDestinationCluster maps one Argo CD destination reference — either a
// `spec.destination.server` URL or a `spec.destination.name` — onto the cluster
// row it targets, using the argocd_managed_clusters registration index for this
// instance. Returns errArgoDestinationUnresolved when nothing matches.
func (h *ArgoCDHandler) resolveDestinationCluster(ctx context.Context, instance sqlc.ArgocdInstance, destination string) (uuid.UUID, error) {
	dest := strings.TrimSpace(destination)
	if dest == "" {
		return uuid.Nil, errArgoDestinationUnresolved
	}
	if _, ok := argoCDInClusterDestinations[dest]; ok {
		if instance.ClusterID == uuid.Nil {
			return uuid.Nil, errArgoDestinationUnresolved
		}
		return instance.ClusterID, nil
	}
	rows, err := h.queries.ListArgoCDManagedClusters(ctx, instance.ID)
	if err != nil {
		return uuid.Nil, err
	}
	// server form first: it is the authoritative key of a registration and
	// needs no extra query.
	for _, row := range rows {
		if trimTrailingSlash(row.ServerUrl) == trimTrailingSlash(dest) {
			return row.ClusterID, nil
		}
	}
	// name form: Argo CD matches the registered cluster's display name, which
	// RegisterManagedCluster takes from cluster.Name. cluster_secret_name is
	// deliberately NOT matched — it is the Kubernetes Secret name, which is
	// derived from the server URL and can collide with an unrelated cluster's
	// display name, and binding the authorization decision to a different
	// cluster than Argo CD will act on is worse than denying.
	for _, row := range rows {
		cluster, err := h.queries.GetClusterByID(ctx, row.ClusterID)
		if err != nil {
			continue
		}
		if strings.TrimSpace(cluster.Name) == dest {
			return row.ClusterID, nil
		}
	}
	return uuid.Nil, errArgoDestinationUnresolved
}

func trimTrailingSlash(v string) string {
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

// authorizeResolvedDestinations requires (workloads, verb) on the cluster every
// reference resolves to. An empty reference set is a denial: "no destination"
// must not read as "no restriction".
func (h *ArgoCDHandler) authorizeResolvedDestinations(w http.ResponseWriter, r *http.Request, bindings []rbac.RoleBinding, instance sqlc.ArgocdInstance, refs []string, verb rbac.Verb) bool {
	if len(refs) == 0 {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	for _, ref := range refs {
		clusterID, err := h.resolveDestinationCluster(r.Context(), instance, ref)
		if err != nil {
			if errors.Is(err, errArgoDestinationUnresolved) {
				respondArgoDestinationForbidden(w, r)
				return false
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to resolve the Argo CD destination cluster")
			return false
		}
		if !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, verb) {
			respondArgoDestinationForbidden(w, r)
			return false
		}
	}
	return true
}

// respondArgoDestinationForbidden keeps the denial indistinguishable between
// "you lack the grant on the destination" and "the destination is unknown", so
// the endpoint cannot be used to probe which clusters are registered.
func respondArgoDestinationForbidden(w http.ResponseWriter, r *http.Request) {
	RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden,
		"You do not have permission to act on this application's destination cluster")
}

// authorizeCachedDestination gates on the destination recorded on the cached
// argocd_applications row. That column is written by discoverArgoCDApplication
// from the LIVE upstream Application, so it already carries the rendered
// destination of an ApplicationSet-generated app rather than a `{{server}}`
// template.
func (h *ArgoCDHandler) authorizeCachedDestination(w http.ResponseWriter, r *http.Request, instance sqlc.ArgocdInstance, app sqlc.ArgocdApplication, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted {
		return true
	}
	return h.authorizeResolvedDestinations(w, r, bindings, instance, []string{app.DestinationCluster}, verb)
}

// authorizeSpecDestination gates on a destination supplied in a request body
// (CreateApplication) . Both `name` and `server` are authorized when both are
// set: upstream rejects that combination, but we must not let a mismatched pair
// pick whichever field is cheapest to satisfy.
func (h *ArgoCDHandler) authorizeSpecDestination(w http.ResponseWriter, r *http.Request, instance sqlc.ArgocdInstance, dest *argocdclient.ApplicationDestination, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted {
		return true
	}
	return h.authorizeResolvedDestinations(w, r, bindings, instance, destinationRefs(dest), verb)
}

// authorizeUpstreamDestination reads the live Application and gates on its
// current destination. Used by the by-name mutation routes (patch, delete),
// where the request carries no destination and the cache may be absent or
// stale. The upstream read is skipped entirely for unrestricted callers.
func (h *ArgoCDHandler) authorizeUpstreamDestination(w http.ResponseWriter, r *http.Request, instance sqlc.ArgocdInstance, name string, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted {
		return true
	}
	live, err := h.argoCDClient(instance).GetApp(r.Context(), name)
	if translateClientError(w, r, err) {
		return false
	}
	if live == nil {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	return h.authorizeResolvedDestinations(w, r, bindings, instance, destinationRefs(live.Spec.Destination), verb)
}

// destinationRefs flattens a destination block into the references that must
// each be authorized. Templated values (`{{server}}` and friends) are dropped:
// they are not resolvable here and are handled by the ApplicationSet fan-out
// check, which authorizes the generator's cluster set instead.
func destinationRefs(dest *argocdclient.ApplicationDestination) []string {
	if dest == nil {
		return nil
	}
	refs := make([]string, 0, 2)
	for _, ref := range []string{dest.Name, dest.Server} {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.Contains(ref, "{{") {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// authorizeOperationDestination gates a durable operation's re-run on the
// destination of the Application it targets. Only application-typed operations
// carry a destination; anything else keeps the instance-scoped check alone.
func (h *ArgoCDHandler) authorizeOperationDestination(w http.ResponseWriter, r *http.Request, op sqlc.ArgocdOperation, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted || op.TargetType != "application" {
		return true
	}
	appID, err := uuid.Parse(op.TargetKey)
	if err != nil {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), appID)
	if err != nil {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), app.ArgocdInstanceID)
	if err != nil {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	return h.authorizeResolvedDestinations(w, r, bindings, instance, []string{app.DestinationCluster}, verb)
}

// patchedApplicationDestination extracts the destination a JSON merge patch
// would install on an Application, or nil when the patch leaves the destination
// CLUSTER alone. A body that will not decode is rejected rather than treated as
// "no destination", so a malformed patch cannot skip the second gate. ok==false
// means the response has already been written.
func patchedApplicationDestination(w http.ResponseWriter, r *http.Request, raw []byte) (*argocdclient.ApplicationDestination, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, true
	}
	var patch struct {
		Spec struct {
			Destination *argocdclient.ApplicationDestination `json:"destination"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &patch); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return nil, false
	}
	dest := patch.Spec.Destination
	// A destination block carrying neither `name` nor `server` — the
	// namespace-only re-target — does not move the Application to another
	// cluster, and the cluster it is on today was authorized by the caller's
	// first gate. Report it as "unchanged" so the second gate is skipped: an
	// empty reference set means "invalid destination" to
	// authorizeResolvedDestinations, which is the right answer for
	// CreateApplication and the wrong one here. A templated or otherwise
	// unresolvable value is deliberately NOT folded in — it still reaches the
	// gate and is denied.
	if dest != nil && strings.TrimSpace(dest.Name) == "" && strings.TrimSpace(dest.Server) == "" {
		return nil, true
	}
	return dest, true
}

// authorizeApplicationSetDestinations gates a submitted ApplicationSet spec
// (creation) on the clusters the set will actually fan out to. The delete route
// gates the same way on the live spec — see
// authorizeUpstreamApplicationSetDestinations below.
//
// Two shapes:
//   - a concrete template destination — every generated Application lands
//     there, so that single destination is the whole blast radius;
//   - a templated destination (the `{{server}}` idiom) — the blast radius is
//     the cluster set the `clusters` generators select, so the caller must hold
//     the verb on every managed cluster the selectors currently match.
//
// A templated destination with no cluster generator (a list or git generator
// naming servers inline) cannot be bounded from the spec, and is denied for
// restricted callers. Superusers keep that shape.
func (h *ArgoCDHandler) authorizeApplicationSetDestinations(w http.ResponseWriter, r *http.Request, instance sqlc.ArgocdInstance, spec argocdclient.ApplicationSetSpec, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted {
		return true
	}
	return h.authorizeApplicationSetSpec(w, r, bindings, instance, spec, verb)
}

// authorizeUpstreamApplicationSetDestinations reads the LIVE ApplicationSet and
// gates on the fan-out it has today. Used by the by-name route (delete), where
// the request carries no spec. Deleting an ApplicationSet cascades through
// ownerReferences to its generated Applications, which carry
// `resources-finalizer.argocd.argoproj.io` (internal/server/baseline_appsets.go),
// so the delete reaches the workloads in every destination the set fanned out
// to — a strictly larger blast radius than the creation this mirrors. The
// upstream read is skipped entirely for unrestricted callers.
func (h *ArgoCDHandler) authorizeUpstreamApplicationSetDestinations(w http.ResponseWriter, r *http.Request, instance sqlc.ArgocdInstance, name string, verb rbac.Verb) bool {
	bindings, restricted, ok := h.destinationAuthzBindings(w, r)
	if !ok {
		return false
	}
	if !restricted {
		return true
	}
	live, err := h.argoCDClient(instance).GetApplicationSet(r.Context(), name)
	if translateClientError(w, r, err) {
		return false
	}
	if live == nil {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	return h.authorizeApplicationSetSpec(w, r, bindings, instance, live.Spec, verb)
}

// authorizeApplicationSetSpec is the shared body of the two entry points above:
// bindings are already loaded and the caller is known to be restricted.
func (h *ArgoCDHandler) authorizeApplicationSetSpec(w http.ResponseWriter, r *http.Request, bindings []rbac.RoleBinding, instance sqlc.ArgocdInstance, spec argocdclient.ApplicationSetSpec, verb rbac.Verb) bool {
	if refs := destinationRefs(spec.Template.Spec.Destination); len(refs) > 0 {
		return h.authorizeResolvedDestinations(w, r, bindings, instance, refs, verb)
	}

	selectors := clusterGeneratorSelectors(spec.Generators, nil)
	if len(selectors) == 0 {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	rows, err := h.queries.ListArgoCDManagedClusters(r.Context(), instance.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to resolve the Argo CD destination cluster")
		return false
	}
	matched := 0
	for _, row := range rows {
		var labels map[string]string
		_ = json.Unmarshal(row.Labels, &labels)
		if !anyLabelSelectorMatches(selectors, labels) {
			continue
		}
		matched++
		if !h.authz.allowsCluster(bindings, row.ClusterID, rbac.ResourceWorkloads, verb) {
			respondArgoDestinationForbidden(w, r)
			return false
		}
	}
	// A selector matching nothing today is denied rather than allowed: the
	// set is live, and the next cluster registered with matching labels would
	// be adopted by it without any further authorization.
	if matched == 0 {
		respondArgoDestinationForbidden(w, r)
		return false
	}
	return true
}

// clusterGeneratorSelectors collects every `clusters` generator selector in the
// spec, descending into matrix and merge children the same way
// validateApplicationSetClusterGenerators does.
func clusterGeneratorSelectors(generators []argocdclient.ApplicationSetGenerator, out []*argocdclient.LabelSelector) []*argocdclient.LabelSelector {
	for _, generator := range generators {
		if generator.Cluster != nil {
			out = append(out, generator.Cluster.Selector)
		}
		if generator.Matrix != nil {
			out = clusterGeneratorSelectors(generator.Matrix.Generators, out)
		}
		if generator.Merge != nil {
			out = clusterGeneratorSelectors(generator.Merge.Generators, out)
		}
	}
	return out
}

func anyLabelSelectorMatches(selectors []*argocdclient.LabelSelector, labels map[string]string) bool {
	for _, selector := range selectors {
		if labelSelectorMatches(selector, labels) {
			return true
		}
	}
	return false
}

// labelSelectorMatches evaluates a Kubernetes label selector against the label
// set recorded for a registration. A nil selector matches everything, matching
// Argo CD's own behaviour for a `clusters` generator with no selector.
func labelSelectorMatches(selector *argocdclient.LabelSelector, labels map[string]string) bool {
	if selector == nil {
		return true
	}
	for key, want := range selector.MatchLabels {
		if labels[key] != want {
			return false
		}
	}
	for _, expr := range selector.MatchExpressions {
		got, present := labels[expr.Key]
		switch expr.Operator {
		case "In":
			if !present || !slices.Contains(expr.Values, got) {
				return false
			}
		case "NotIn":
			if present && slices.Contains(expr.Values, got) {
				return false
			}
		case "Exists":
			if !present {
				return false
			}
		case "DoesNotExist":
			if present {
				return false
			}
		default:
			// An operator we do not model cannot be evaluated, so the
			// selected set is unknown — deny rather than over-match.
			return false
		}
	}
	return true
}
