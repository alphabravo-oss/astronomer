// Package agent: managed-side decommission handler.
//
// The server-side cluster decommission reconciler (see
// internal/worker/tasks/cluster_decommission.go) sends a MsgDecommission
// over the tunnel. This file is what answers that message — it deletes the
// managed-side resources that astronomer-go installed in the user's cluster
// (Fluent Bit DaemonSets via the astronomer-logging namespace, our labeled
// Velero CRs, the agent's own Deployment) and ACKs with a per-step report.
//
// Safety contract:
//
//  1. We NEVER hard-delete the "velero" or "monitoring" namespaces themselves
//     — the cluster operator may have other things there. We only remove
//     resources we labeled astronomer.io/managed=true.
//  2. The agent Deployment is the LAST thing deleted, AFTER sending the
//     ACK. (If we delete it before sending the ACK, the kubelet may tear
//     us down mid-write and the server never sees the result.)
//  3. Errors on individual resource deletes are reported in the ACK but
//     don't halt subsequent steps — partial cleanup is better than no
//     cleanup, and the operator can fix the rest manually using the per-
//     step error.
//  4. DryRun mode is honoured for the integration test path; nothing is
//     actually deleted but the counts reflect what WOULD be deleted.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// DefaultAgentNamespace is the namespace the agent is installed into by
// the manifest in deploy/agent/. Used as fallback when the server's
// MsgDecommission payload doesn't override it.
const DefaultAgentNamespace = "astronomer-system"

// DefaultAgentDeploymentName is the Deployment our manifest creates.
const DefaultAgentDeploymentName = "astronomer-agent"

// DefaultLoggingNamespace is what the logging stack lives in. This whole
// namespace is removed by the decommission flow — Fluent Bit + log
// forwarders + any ConfigMaps are tied to it.
const DefaultLoggingNamespace = "astronomer-logging"

// veleroNamespace is where Velero typically installs. We DO NOT delete this
// namespace; only Backup/Schedule/Restore CRs labeled astronomer.io/managed=true
// inside it.
const veleroNamespace = "velero"

// Default label selectors used when the server's DecommissionPayload leaves
// the override fields empty (back-compat with an older server). Verified
// against deploy/agent/install.yaml.template:
//   - namespaces carry app.kubernetes.io/managed-by=astronomer-server (the
//     namespace-creation contract: an operator-precreated namespace lacks it
//     and is therefore NEVER deleted);
//   - the cluster-scoped RBAC (astronomer-agent + astronomer-kube-state-metrics
//     ClusterRole/Binding) and the namespaced singletons all carry
//     app.kubernetes.io/part-of=astronomer (the agent RBAC does NOT carry
//     managed-by, so gating on managed-by would silently ORPHAN it);
//   - Velero CRs carry app.kubernetes.io/managed-by=astronomer-go.
const (
	defaultVeleroSelector    = "app.kubernetes.io/managed-by=astronomer-go"
	defaultManagedBySelector = "app.kubernetes.io/managed-by=astronomer-server"
	defaultRBACSelector      = "app.kubernetes.io/part-of=astronomer"
)

// baselineNamespaces are the 6 namespaces the agent install creates in
// ADDITION to astronomer-system (which is torn down LAST, after the ACK, by
// the deferred self-delete). Each is gated on the managed-by label before
// deletion so a pre-existing operator namespace of the same name is untouched.
var baselineNamespaces = []string{
	"astronomer-monitoring",
	"astronomer-trivy-system",
	"astronomer-logging",
	"astronomer-ingress-nginx",
	"astronomer-cert-manager",
	"astronomer-gatekeeper-system",
}

// hasLabel is the single safety chokepoint: a delete is only ever issued for a
// live object whose label matches what astronomer-go itself stamps. No blind
// delete-by-name path exists.
//
// FAIL CLOSED on an empty key: splitSelector returns ("","") for a
// malformed/empty selector, and labels[""]=="" is true for EVERY object — so
// without this guard a bad selector would match (and delete) every
// hardcoded-name target unguarded, the exact over-deletion landmine this guard
// exists to prevent. An empty key never matches.
func hasLabel(labels map[string]string, key, val string) bool {
	if key == "" {
		return false
	}
	return labels[key] == val
}

// splitSelector parses a "key=value" label selector into its key and value.
// Returns ("","") for a malformed/empty selector so the guard fails closed
// (nothing matches → nothing deleted).
func splitSelector(sel string) (key, val string) {
	k, v, ok := strings.Cut(sel, "=")
	if !ok {
		return "", ""
	}
	return k, v
}

// DecommissionHandler handles MsgDecommission messages.
type DecommissionHandler struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	log           *slog.Logger
	// agentDeleteDelay is how long we wait after sending the ACK before
	// deleting the agent's own Deployment. The delay gives the WS write
	// loop time to flush the ACK frame to the server.
	agentDeleteDelay time.Duration
	// pause, when set, is flipped true at the start of decommission so the pull
	// reconcile loop stops re-creating resources we're tearing down.
	pause *atomic.Bool
}

// SetPauseGuard wires the shared reconcile-pause flag (see ReconcileHandler).
func (h *DecommissionHandler) SetPauseGuard(g *atomic.Bool) {
	if h != nil {
		h.pause = g
	}
}

// NewDecommissionHandler builds a handler from the shared K8sProxy client
// and rest.Config. The dynamic client is needed for Velero CR cleanup
// (Backups, Schedules, Restores) since those aren't typed in the agent's
// dependency set.
func NewDecommissionHandler(client kubernetes.Interface, restCfg *rest.Config, log *slog.Logger) (*DecommissionHandler, error) {
	if log == nil {
		log = slog.Default()
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return &DecommissionHandler{
		clientset:        client,
		dynamicClient:    dyn,
		log:              log,
		agentDeleteDelay: 2 * time.Second,
	}, nil
}

// HandleDecommission is the MessageHandler entrypoint registered for
// MsgDecommission. It returns the MsgDecommissionAck synchronously; the
// caller (tunnel readLoop) routes that response back via stream_id.
//
// After the ACK is queued, the handler schedules the agent's own Deployment
// for deletion on a short delay so the WS write loop has a chance to flush.
func (h *DecommissionHandler) HandleDecommission(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	if h == nil {
		return nil, fmt.Errorf("decommission handler not configured")
	}
	// Halt the pull reconcile loop FIRST so its Phase-2 self-apply can't
	// re-create the agent Deployment we're about to delete.
	if h.pause != nil {
		h.pause.Store(true)
	}
	var req protocol.DecommissionPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("decode decommission payload: %w", err)
	}

	steps := []protocol.DecommissionStepResult{}

	// Velero CRs are removed in both the legacy and full-footprint flows.
	if req.RemoveVeleroManaged {
		veleroSelector := req.VeleroLabel
		if veleroSelector == "" {
			// Back-compat: a legacy server may send the old astronomer.io
			// selector via ManagedLabel; prefer the new field, fall back to
			// ManagedLabel, then to the corrected default.
			veleroSelector = req.ManagedLabel
		}
		if veleroSelector == "" {
			veleroSelector = defaultVeleroSelector
		}
		steps = append(steps, h.removeVeleroManaged(ctx, veleroSelector, req.DryRun))
	} else {
		steps = append(steps, protocol.DecommissionStepResult{
			Name:    "remove_velero_managed",
			Skipped: true,
		})
	}

	if req.RemoveFullFootprint {
		// Full-footprint teardown. The standalone logging-stack delete is
		// SUBSUMED by remove_baseline_namespaces (astronomer-logging is one of
		// the baseline namespaces) — report it Skipped to avoid a double
		// delete and to keep the ACK step set stable for the server.
		steps = append(steps, protocol.DecommissionStepResult{
			Name:    "remove_logging_stack",
			Skipped: true,
		})
		nsKey, nsVal := splitSelector(orDefault(req.ManagedByLabel, defaultManagedBySelector))
		rbacKey, rbacVal := splitSelector(orDefault(req.RBACLabel, defaultRBACSelector))
		steps = append(steps, h.removeBaselineNamespaces(ctx, nsKey, nsVal, req.DryRun))
		steps = append(steps, h.removeClusterRBAC(ctx, rbacKey, rbacVal, req.DryRun))
		steps = append(steps, h.removeAgentSingletons(ctx, h.agentNamespace(req), rbacKey, rbacVal, req.DryRun))
	} else if req.RemoveLoggingStack {
		steps = append(steps, h.removeLoggingStack(ctx, req.DryRun))
	} else {
		steps = append(steps, protocol.DecommissionStepResult{
			Name:    "remove_logging_stack",
			Skipped: true,
		})
	}

	// Build and queue the ACK. The agent Deployment delete (step below) is
	// scheduled on a delay so the WS write loop can flush this frame first.
	ackStep := protocol.DecommissionStepResult{Name: "remove_agent_deployment"}
	if !req.RemoveAgentDeployment {
		ackStep.Skipped = true
	} else if req.DryRun {
		ackStep.Success = true
		ackStep.Removed = 1
	} else {
		// We mark the step "success: pending" so the server knows we
		// scheduled the delete; the actual kubectl delete happens after
		// the ACK is queued for writing.
		ackStep.Success = true
		ackStep.Removed = 1
	}
	steps = append(steps, ackStep)

	ack := protocol.DecommissionAckPayload{
		ClusterID: req.ClusterID,
		DryRun:    req.DryRun,
		Steps:     steps,
	}
	body, err := json.Marshal(ack)
	if err != nil {
		return nil, fmt.Errorf("marshal decommission ack: %w", err)
	}
	resp := &protocol.Message{
		Type:      protocol.MsgDecommissionAck,
		StreamID:  msg.StreamID,
		RequestID: msg.RequestID,
		ClusterID: msg.ClusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}

	if req.RemoveAgentDeployment && !req.DryRun {
		// Fire-and-forget the agent self-delete after a short delay. We
		// deliberately use context.Background here: if the caller's ctx
		// is cancelled (which it will be when our pod is terminating!) we
		// don't want that to interrupt the API server delete request mid-
		// flight.
		ns := h.agentNamespace(req)
		name := req.AgentDeployment
		if name == "" {
			name = DefaultAgentDeploymentName
		}
		delay := h.agentDeleteDelay
		removeNamespace := req.RemoveFullFootprint
		nsKey, nsVal := splitSelector(orDefault(req.ManagedByLabel, defaultManagedBySelector))
		rbacKey, rbacVal := splitSelector(orDefault(req.RBACLabel, defaultRBACSelector))
		go func() {
			time.Sleep(delay)
			deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err := h.clientset.AppsV1().Deployments(ns).Delete(deleteCtx, name, kubeutil.DeleteOptions())
			if err != nil && !apierrors.IsNotFound(err) {
				h.log.Warn("decommission: failed to delete agent deployment",
					"namespace", ns, "name", name, "error", err)
			} else {
				h.log.Info("decommission: agent deployment deleted",
					"namespace", ns, "name", name)
			}
			if !removeNamespace {
				return
			}
			// astronomer-system is torn down LAST: it cascades the SA,
			// Secrets, ConfigMap, Service, NetworkPolicy, PDB, the
			// self-management Roles/RoleBindings, and terminates the agent
			// pod. Strictly label-gated so an operator namespace of the same
			// name (lacking managed-by) is never deleted. A Forbidden here
			// (non-admin profile) is non-fatal: the Deployment delete above
			// already terminated the pod; the server's orphan audit surfaces
			// the residual (now near-empty) namespace.
			obj, gerr := h.clientset.CoreV1().Namespaces().Get(deleteCtx, ns, metav1.GetOptions{})
			if gerr != nil {
				if !apierrors.IsNotFound(gerr) {
					h.log.Warn("decommission: failed to load agent namespace for teardown",
						"namespace", ns, "error", gerr)
				}
				return
			}
			if !hasLabel(obj.Labels, nsKey, nsVal) {
				h.log.Warn("decommission: agent namespace not astronomer-managed, leaving in place",
					"namespace", ns)
				return
			}
			if derr := h.clientset.CoreV1().Namespaces().Delete(deleteCtx, ns, kubeutil.DeleteOptions()); derr != nil && !apierrors.IsNotFound(derr) {
				h.log.Warn("decommission: failed to delete agent namespace",
					"namespace", ns, "error", derr)
			} else {
				h.log.Info("decommission: agent namespace deleted", "namespace", ns)
			}
			// Finally, the agent's OWN cluster-scoped RBAC — deleted LAST, after
			// the namespace delete that needed it (removing it sooner would
			// self-revoke the namespace-delete permission). Best-effort: the pod
			// is terminating, but the cluster-scoped role/binding outlive the
			// namespace cascade and a residual is caught by the server orphan audit.
			h.removeAgentClusterRBAC(deleteCtx, rbacKey, rbacVal)
		}()
	}

	return resp, nil
}

// agentNamespace resolves the agent's namespace from the payload, falling back
// to the install default.
func (h *DecommissionHandler) agentNamespace(req protocol.DecommissionPayload) string {
	if req.AgentNamespace != "" {
		return req.AgentNamespace
	}
	return DefaultAgentNamespace
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// removeLoggingStack deletes the entire astronomer-logging namespace.
// Fluent Bit DaemonSets, log forwarder ConfigMaps, ServiceAccounts and any
// other resources we installed under it go away with the namespace.
// We DON'T touch the cluster operator's own log collectors that live in
// other namespaces.
func (h *DecommissionHandler) removeLoggingStack(ctx context.Context, dryRun bool) protocol.DecommissionStepResult {
	step := protocol.DecommissionStepResult{Name: "remove_logging_stack"}
	if dryRun {
		// Check whether the namespace exists; reported "removed" is 0 or 1.
		_, err := h.clientset.CoreV1().Namespaces().Get(ctx, DefaultLoggingNamespace, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				step.Success = true
				return step
			}
			step.Error = err.Error()
			return step
		}
		step.Success = true
		step.Removed = 1
		return step
	}
	err := h.clientset.CoreV1().Namespaces().Delete(ctx, DefaultLoggingNamespace, kubeutil.DeleteOptions())
	if err != nil {
		if apierrors.IsNotFound(err) {
			step.Success = true
			return step
		}
		step.Error = err.Error()
		return step
	}
	step.Success = true
	step.Removed = 1
	return step
}

// veleroGVRs are the Velero CRDs we hard-delete when astronomer.io/managed=true.
// We don't touch other CRDs or the velero namespace itself.
var veleroGVRs = []schema.GroupVersionResource{
	{Group: "velero.io", Version: "v1", Resource: "backups"},
	{Group: "velero.io", Version: "v1", Resource: "schedules"},
	{Group: "velero.io", Version: "v1", Resource: "restores"},
}

// veleroBSLGVR is the BackupStorageLocation CR. We LIST (never delete) these
// during decommission: deleting a BSL does NOT remove the backing cloud blobs,
// so we surface the residuals as Orphans for the server's orphan-audit event.
var veleroBSLGVR = schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backupstoragelocations"}

// removeVeleroManaged deletes Velero Backup/Schedule/Restore CRs matching
// labelSelector (default app.kubernetes.io/managed-by=astronomer-go) from the
// velero namespace. The namespace itself stays intact. BackupStorageLocations
// matching the same selector are LISTED into step.Orphans (not deleted) so the
// server can emit an orphan-audit event for manual cloud-side cleanup.
func (h *DecommissionHandler) removeVeleroManaged(ctx context.Context, labelSelector string, dryRun bool) protocol.DecommissionStepResult {
	step := protocol.DecommissionStepResult{Name: "remove_velero_managed"}
	if h.dynamicClient == nil {
		step.Error = "dynamic client not configured"
		return step
	}
	totalRemoved := 0
	errs := []string{}
	for _, gvr := range veleroGVRs {
		list, err := h.dynamicClient.Resource(gvr).Namespace(veleroNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			if apierrors.IsNotFound(err) {
				// CRD not installed — silent skip.
				continue
			}
			errs = append(errs, fmt.Sprintf("list %s: %v", gvr.Resource, err))
			continue
		}
		if dryRun {
			totalRemoved += len(list.Items)
			continue
		}
		for _, item := range list.Items {
			if err := h.dynamicClient.Resource(gvr).Namespace(veleroNamespace).Delete(ctx, item.GetName(), kubeutil.DeleteOptions()); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				errs = append(errs, fmt.Sprintf("delete %s/%s: %v", gvr.Resource, item.GetName(), err))
				continue
			}
			totalRemoved++
		}
	}
	// Orphan audit: list (don't delete) managed BackupStorageLocations.
	if bsls, err := h.dynamicClient.Resource(veleroBSLGVR).Namespace(veleroNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	}); err == nil {
		for _, item := range bsls.Items {
			step.Orphans = append(step.Orphans, item.GetName())
		}
	} else if !apierrors.IsNotFound(err) {
		errs = append(errs, fmt.Sprintf("list backupstoragelocations: %v", err))
	}
	step.Removed = totalRemoved
	if len(errs) > 0 {
		step.Error = errors.Join(toErrors(errs)...).Error()
		// Partial success: we may have removed some CRs even with errors;
		// reflect that in the count but not in Success.
		step.Success = false
		return step
	}
	step.Success = true
	return step
}

// guardOutcome accumulates the per-target results of a label-gated group
// delete (remove_baseline_namespaces / remove_cluster_rbac /
// remove_agent_singletons). guarded captures targets skipped because their
// live label didn't match (NOT ours → never deleted); forbidden captures 403s
// (non-admin profile — the documented cleanup-gap); errs captures genuine API
// failures.
type guardOutcome struct {
	removed   int
	guarded   []string
	forbidden []string
	errs      []string
}

// guardedDelete is the airtight per-target path: NotFound (already gone) is a
// success; a label mismatch is a hard SKIP (no delete issued); Forbidden is a
// reported skip; anything else is a reported error. del is only ever invoked
// AFTER the live label assertion passes.
func (o *guardOutcome) guardedDelete(kind, name string, labels map[string]string, getErr error, key, val string, dryRun bool, del func() error) {
	if getErr != nil {
		if apierrors.IsNotFound(getErr) {
			return // already gone → idempotent success
		}
		if apierrors.IsForbidden(getErr) {
			o.forbidden = append(o.forbidden, fmt.Sprintf("forbidden: get %s/%s", kind, name))
			return
		}
		o.errs = append(o.errs, fmt.Sprintf("get %s/%s: %v", kind, name, getErr))
		return
	}
	if !hasLabel(labels, key, val) {
		o.guarded = append(o.guarded, fmt.Sprintf("label guard: %s/%s not astronomer-managed", kind, name))
		return
	}
	if dryRun {
		o.removed++
		return
	}
	if err := del(); err != nil {
		if apierrors.IsNotFound(err) {
			o.removed++
			return
		}
		if apierrors.IsForbidden(err) {
			o.forbidden = append(o.forbidden, fmt.Sprintf("forbidden: delete %s/%s", kind, name))
			return
		}
		o.errs = append(o.errs, fmt.Sprintf("delete %s/%s: %v", kind, name, err))
		return
	}
	o.removed++
}

// toStep renders the accumulated outcome as a DecommissionStepResult.
func (o *guardOutcome) toStep(name string) protocol.DecommissionStepResult {
	step := protocol.DecommissionStepResult{Name: name, Removed: o.removed}
	notes := make([]string, 0, len(o.guarded)+len(o.forbidden))
	notes = append(notes, o.guarded...)
	notes = append(notes, o.forbidden...)
	if len(o.errs) > 0 {
		step.Success = false
		step.Error = strings.Join(append(append([]string{}, notes...), o.errs...), "; ")
		return step
	}
	step.Success = true
	if len(notes) > 0 {
		step.Error = strings.Join(notes, "; ")
	}
	// Nothing removed but targets were guarded/forbidden → the step did no
	// destructive work; mark Skipped so the server records it as such.
	if o.removed == 0 && len(notes) > 0 {
		step.Skipped = true
	}
	return step
}

// removeBaselineNamespaces deletes the 6 baseline namespaces (NOT
// astronomer-system, which is torn down last by the deferred self-delete).
// Each is gated on the managed-by label so a pre-existing operator namespace
// of the same name is never deleted; contents cascade with the namespace.
func (h *DecommissionHandler) removeBaselineNamespaces(ctx context.Context, key, val string, dryRun bool) protocol.DecommissionStepResult {
	o := &guardOutcome{}
	for _, ns := range baselineNamespaces {
		obj, err := h.clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		var labels map[string]string
		if obj != nil {
			labels = obj.Labels
		}
		o.guardedDelete("namespace", ns, labels, err, key, val, dryRun, func() error {
			return h.clientset.CoreV1().Namespaces().Delete(ctx, ns, kubeutil.DeleteOptions())
		})
	}
	return o.toStep("remove_baseline_namespaces")
}

// removeClusterRBAC deletes the cluster-scoped kube-state-metrics RBAC. It does
// NOT delete the agent's OWN astronomer-agent ClusterRole/ClusterRoleBinding —
// those grant the agent the cluster-scoped permission it needs to delete the
// astronomer-system namespace, so deleting them here would self-revoke and make
// the deferred namespace teardown fail (Forbidden). The agent's own cluster RBAC
// is removed LAST, after the namespace delete, by removeAgentClusterRBAC in the
// deferred self-delete goroutine. The agent's namespaced RoleBinding variant
// (used by namespaced profiles) cascades with the namespace delete; we also
// attempt it here for the case the namespace delete is Forbidden. Gated on
// part-of=astronomer.
func (h *DecommissionHandler) removeClusterRBAC(ctx context.Context, key, val string, dryRun bool) protocol.DecommissionStepResult {
	o := &guardOutcome{}
	rbac := h.clientset.RbacV1()

	for _, name := range []string{"astronomer-kube-state-metrics"} {
		// ClusterRole
		cr, err := rbac.ClusterRoles().Get(ctx, name, metav1.GetOptions{})
		var crLabels map[string]string
		if cr != nil {
			crLabels = cr.Labels
		}
		o.guardedDelete("clusterrole", name, crLabels, err, key, val, dryRun, func() error {
			return rbac.ClusterRoles().Delete(ctx, name, kubeutil.DeleteOptions())
		})
		// ClusterRoleBinding
		crb, err := rbac.ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
		var crbLabels map[string]string
		if crb != nil {
			crbLabels = crb.Labels
		}
		o.guardedDelete("clusterrolebinding", name, crbLabels, err, key, val, dryRun, func() error {
			return rbac.ClusterRoleBindings().Delete(ctx, name, kubeutil.DeleteOptions())
		})
	}

	// The astronomer-agent binding may instead be a namespaced RoleBinding in
	// astronomer-system. NotFound (the ClusterRoleBinding variant was used) is
	// a no-op.
	rb, err := rbac.RoleBindings(DefaultAgentNamespace).Get(ctx, "astronomer-agent", metav1.GetOptions{})
	var rbLabels map[string]string
	if rb != nil {
		rbLabels = rb.Labels
	}
	o.guardedDelete("rolebinding", "astronomer-agent", rbLabels, err, key, val, dryRun, func() error {
		return rbac.RoleBindings(DefaultAgentNamespace).Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
	})

	return o.toStep("remove_cluster_rbac")
}

// removeAgentClusterRBAC deletes the agent's OWN cluster-scoped RBAC
// (astronomer-agent ClusterRole + ClusterRoleBinding), gated on part-of. It runs
// LAST — in the deferred self-delete goroutine, AFTER the astronomer-system
// namespace delete — because these grant the cluster-scoped permission the
// namespace delete needs; removing them sooner self-revokes it. Best-effort (the
// pod is terminating); a residual is surfaced by the server's orphan audit.
func (h *DecommissionHandler) removeAgentClusterRBAC(ctx context.Context, key, val string) {
	rbac := h.clientset.RbacV1()
	o := &guardOutcome{}
	// ClusterRoleBinding (the actual GRANT) FIRST: this runs in the dying pod's
	// grace window racing process exit, and the binding is the security-relevant
	// half — a lone ClusterRole with no binding (and the SA already gone with the
	// namespace) grants nothing. If exit cuts us off after this, the residual is
	// inert and the server orphan audit surfaces it.
	crb, gerr := rbac.ClusterRoleBindings().Get(ctx, "astronomer-agent", metav1.GetOptions{})
	var crbLabels map[string]string
	if crb != nil {
		crbLabels = crb.Labels
	}
	o.guardedDelete("clusterrolebinding", "astronomer-agent", crbLabels, gerr, key, val, false, func() error {
		return rbac.ClusterRoleBindings().Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
	})
	cr, err := rbac.ClusterRoles().Get(ctx, "astronomer-agent", metav1.GetOptions{})
	var crLabels map[string]string
	if cr != nil {
		crLabels = cr.Labels
	}
	o.guardedDelete("clusterrole", "astronomer-agent", crLabels, err, key, val, false, func() error {
		return rbac.ClusterRoles().Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
	})
	if len(o.guarded)+len(o.errs)+len(o.forbidden) > 0 {
		h.log.Info("decommission: agent cluster RBAC teardown (deferred)",
			"removed", o.removed, "guarded", o.guarded, "errs", o.errs, "forbidden", o.forbidden)
	}
}

// removeAgentSingletons deletes the namespaced singletons in astronomer-system
// in credential-first priority: active identity, bootstrap, and legacy token
// Secrets first, then the CA Secret, ConfigMap, Service, NetworkPolicy, PDB,
// ServiceAccount. Each gated
// on part-of=astronomer. (These would also cascade with the astronomer-system
// namespace delete, but that delete needs cluster-scoped permission a non-admin
// profile lacks.) Whether the namespaced delete here succeeds depends on the
// profile's self-management Role grants; a profile that cannot delete the Secret
// reports Forbidden (captured as a step, non-fatal). Credential material is NOT
// left usable regardless: the server defers and runs revoke_agent_token, which
// hard-revokes the durable token DB-side so residual Secrets are inert.
func (h *DecommissionHandler) removeAgentSingletons(ctx context.Context, ns, key, val string, dryRun bool) protocol.DecommissionStepResult {
	o := &guardOutcome{}
	core := h.clientset.CoreV1()

	// Credential Secrets — active identity first — then the CA bundle.
	for _, secret := range []string{
		"astronomer-agent-identity",
		"astronomer-agent-registration-token",
		"astronomer-agent-token",
		"astronomer-agent-ca",
	} {
		obj, err := core.Secrets(ns).Get(ctx, secret, metav1.GetOptions{})
		var labels map[string]string
		if obj != nil {
			labels = obj.Labels
		}
		o.guardedDelete("secret", secret, labels, err, key, val, dryRun, func() error {
			return core.Secrets(ns).Delete(ctx, secret, kubeutil.DeleteOptions())
		})
	}

	// ConfigMap astronomer-agent-config
	if cm, err := core.ConfigMaps(ns).Get(ctx, "astronomer-agent-config", metav1.GetOptions{}); true {
		var labels map[string]string
		if cm != nil {
			labels = cm.Labels
		}
		o.guardedDelete("configmap", "astronomer-agent-config", labels, err, key, val, dryRun, func() error {
			return core.ConfigMaps(ns).Delete(ctx, "astronomer-agent-config", kubeutil.DeleteOptions())
		})
	}

	// Service astronomer-agent
	if svc, err := core.Services(ns).Get(ctx, "astronomer-agent", metav1.GetOptions{}); true {
		var labels map[string]string
		if svc != nil {
			labels = svc.Labels
		}
		o.guardedDelete("service", "astronomer-agent", labels, err, key, val, dryRun, func() error {
			return core.Services(ns).Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
		})
	}

	// NetworkPolicy astronomer-agent
	if np, err := h.clientset.NetworkingV1().NetworkPolicies(ns).Get(ctx, "astronomer-agent", metav1.GetOptions{}); true {
		var labels map[string]string
		if np != nil {
			labels = np.Labels
		}
		o.guardedDelete("networkpolicy", "astronomer-agent", labels, err, key, val, dryRun, func() error {
			return h.clientset.NetworkingV1().NetworkPolicies(ns).Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
		})
	}

	// PodDisruptionBudget astronomer-agent
	if pdb, err := h.clientset.PolicyV1().PodDisruptionBudgets(ns).Get(ctx, "astronomer-agent", metav1.GetOptions{}); true {
		var labels map[string]string
		if pdb != nil {
			labels = pdb.Labels
		}
		o.guardedDelete("poddisruptionbudget", "astronomer-agent", labels, err, key, val, dryRun, func() error {
			return h.clientset.PolicyV1().PodDisruptionBudgets(ns).Delete(ctx, "astronomer-agent", kubeutil.DeleteOptions())
		})
	}

	// ServiceAccount astronomer-agent is deliberately NOT deleted here. It is the
	// agent pod's OWN local-API identity (its projected SA token) — deleting it
	// synchronously revokes the agent's k8s API access mid-decommission, so the
	// deferred Deployment + namespace + cluster-RBAC teardown then fails
	// Unauthorized and orphans everything. The SA cascades with the
	// astronomer-system namespace delete (the final, self-terminating op), by
	// which point the agent no longer needs API access.

	return o.toStep("remove_agent_singletons")
}

func toErrors(strs []string) []error {
	out := make([]error, 0, len(strs))
	for _, s := range strs {
		out = append(out, errors.New(s))
	}
	return out
}
