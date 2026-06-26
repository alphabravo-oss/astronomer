// Package agent: Fleet-style PULL reconcile loop (agent side).
//
// This is the agent half of the pull-based GitOps reconcile path. When the
// PullReconcileEnabled flag is set (OFF by default — see AgentConfig), the
// agent becomes the LOCAL applier of its own desired state:
//
//  1. Periodically (PullReconcileInterval, ~default) and on a tunnel push
//     notification (MsgDesiredStateResponse arriving unsolicited), the loop
//     requests its DESIRED STATE from the management plane over the existing
//     WS tunnel (MsgDesiredStateRequest, request/response by RequestID).
//  2. The server replies with a set of rendered manifests (the agent's own
//     Deployment/config + enabled baseline component namespaces), each bounded
//     to an astronomer-* namespace and carrying a revision hash.
//  3. The agent server-side-applies each manifest LOCALLY with its in-cluster
//     kube client, stamping the managed-by label on every applied object.
//  4. The agent PRUNES: it deletes managed-by-labeled objects in the
//     astronomer-* namespaces that are no longer in the desired set.
//  5. The agent reports the outcome back via MsgApplyStatus (fire-and-forget).
//
// SAFETY CONTRACT (non-negotiable):
//
//   - Apply and prune are bounded to AstronomerOwnedNamespaces ONLY. Any
//     manifest whose namespace is NOT in that set is REFUSED (never applied),
//     and prune only ever lists/deletes within those namespaces.
//   - Prune is scoped by the managed-by label selector, so an unmanaged object
//     living in an astronomer-* namespace is never touched.
//   - Cluster-scoped objects (no namespace) are refused — the agent only owns
//     the namespaced footprint of the astronomer-* namespaces.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	k8syaml "sigs.k8s.io/yaml"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// pullFieldManager is the field-manager / owner identity the pull reconcile
// applier stamps. Distinct from any other applier so ownership is attributable.
const pullFieldManager = "astronomer-agent-pull"

// defaultPullReconcileInterval is the fallback loop period when the configured
// interval is non-positive.
const defaultPullReconcileInterval = 60 * time.Second

// GVRResolver maps a GroupVersionKind to the GroupVersionResource the dynamic
// client addresses, plus whether the resource is namespaced. Production wires a
// RESTMapper-backed resolver; tests inject a static one so the fake dynamic
// client needs no discovery.
type GVRResolver func(gvk schema.GroupVersionKind) (gvr schema.GroupVersionResource, namespaced bool, err error)

// restMapperResolver builds a GVRResolver from a live RESTMapper.
func restMapperResolver(mapper meta.RESTMapper) GVRResolver {
	return func(gvk schema.GroupVersionKind) (schema.GroupVersionResource, bool, error) {
		m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return schema.GroupVersionResource{}, false, err
		}
		namespaced := m.Scope != nil && m.Scope.Name() == meta.RESTScopeNameNamespace
		return m.Resource, namespaced, nil
	}
}

// ReconcileHandler runs the agent-side pull reconcile loop.
type ReconcileHandler struct {
	dyn       dynamic.Interface
	resolve   GVRResolver
	log       *slog.Logger
	clusterID string

	// respCh receives MsgDesiredStateResponse frames routed from the tunnel
	// readLoop (via the registered handler). Buffered so an unsolicited push
	// while no request is in flight doesn't block the readLoop.
	respCh chan *protocol.Message
}

// NewReconcileHandler builds a reconcile handler from the agent's in-cluster
// rest.Config. It constructs a dynamic client and a RESTMapper-backed GVR
// resolver. Returns an error if either cannot be built.
func NewReconcileHandler(restCfg *rest.Config, clusterID string, log *slog.Logger) (*ReconcileHandler, error) {
	if log == nil {
		log = slog.Default()
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	mapper, err := kubeutil.RESTMapperForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("rest mapper: %w", err)
	}
	return newReconcileHandler(dyn, restMapperResolver(mapper), clusterID, log), nil
}

// newReconcileHandler is the internal constructor used by both the production
// path and unit tests (which inject a fake dynamic client + static resolver).
func newReconcileHandler(dyn dynamic.Interface, resolve GVRResolver, clusterID string, log *slog.Logger) *ReconcileHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ReconcileHandler{
		dyn:       dyn,
		resolve:   resolve,
		log:       log,
		clusterID: clusterID,
		respCh:    make(chan *protocol.Message, 4),
	}
}

// HandleDesiredStateResponse is the registered MessageHandler for
// MsgDesiredStateResponse. It routes the response to the in-flight requester
// (or to the push-notification path). It is a one-way handler — it returns no
// reply. If no requester is waiting (channel full), the frame is dropped; the
// next periodic tick re-requests, so no state is lost.
func (h *ReconcileHandler) HandleDesiredStateResponse(_ context.Context, msg *protocol.Message) (*protocol.Message, error) {
	select {
	case h.respCh <- msg:
	default:
		h.log.Debug("desired-state response dropped: no requester waiting")
	}
	return nil, nil
}

// Run drives the reconcile loop until ctx is cancelled. It reconciles once
// immediately, then on every interval tick AND whenever a desired-state
// response arrives unsolicited (server push notification). sendFn is the tunnel
// Send method (async, fire-and-forget queue).
func (h *ReconcileHandler) Run(ctx context.Context, interval time.Duration, sendFn func(*protocol.Message) error) {
	if interval <= 0 {
		interval = defaultPullReconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	h.log.Info("pull reconcile loop started", "interval", interval.String(), "cluster_id", h.clusterID)

	// Reconcile once on startup.
	h.reconcileOnce(ctx, sendFn)

	for {
		select {
		case <-ctx.Done():
			h.log.Info("pull reconcile loop stopped")
			return
		case <-ticker.C:
			h.reconcileOnce(ctx, sendFn)
		case msg := <-h.respCh:
			// Server-pushed desired state (unsolicited). Apply directly
			// without a fresh request round-trip.
			h.applyResponse(ctx, msg, sendFn)
		}
	}
}

// reconcileOnce sends a desired-state request and applies whatever response
// comes back within a bounded wait. Drains stale responses first so a request's
// reply isn't shadowed by an earlier push.
func (h *ReconcileHandler) reconcileOnce(ctx context.Context, sendFn func(*protocol.Message) error) {
	req := protocol.DesiredStateRequestPayload{ClusterID: h.clusterID}
	body, err := json.Marshal(req)
	if err != nil {
		h.log.Error("encode desired-state request", "error", err)
		return
	}
	reqID := h.clusterID + "-" + time.Now().UTC().Format("20060102T150405.000000000")
	msg := &protocol.Message{
		Type:      protocol.MsgDesiredStateRequest,
		StreamID:  reqID,
		RequestID: reqID,
		ClusterID: h.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   body,
	}
	if err := sendFn(msg); err != nil {
		h.log.Error("send desired-state request", "error", err)
		return
	}

	select {
	case <-ctx.Done():
		return
	case resp := <-h.respCh:
		h.applyResponse(ctx, resp, sendFn)
	case <-time.After(30 * time.Second):
		h.log.Warn("desired-state request timed out", "request_id", reqID)
	}
}

// applyResponse decodes a desired-state response, applies + prunes, and reports
// status. A server-side render error (Error set) is logged and skipped — the
// agent leaves the cluster footprint untouched rather than acting on a bad set.
func (h *ReconcileHandler) applyResponse(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) {
	var resp protocol.DesiredStateResponsePayload
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		h.log.Error("decode desired-state response", "error", err)
		return
	}
	if msg.Error != "" {
		h.log.Warn("server returned desired-state error; skipping apply", "error", msg.Error, "revision", resp.Revision)
		return
	}

	status := h.Reconcile(ctx, resp)

	statusBody, err := json.Marshal(status)
	if err != nil {
		h.log.Error("encode apply status", "error", err)
		return
	}
	if err := sendFn(&protocol.Message{
		Type:      protocol.MsgApplyStatus,
		ClusterID: h.clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   statusBody,
	}); err != nil {
		h.log.Error("send apply status", "error", err)
	}
}

// Reconcile applies the desired set and prunes managed objects no longer
// desired, returning the aggregate status. Exported so unit tests can drive it
// directly without a tunnel. The cluster ID on the returned status echoes the
// handler's cluster ID.
func (h *ReconcileHandler) Reconcile(ctx context.Context, desired protocol.DesiredStateResponsePayload) protocol.ApplyStatusPayload {
	status := protocol.ApplyStatusPayload{
		ClusterID: h.clusterID,
		Revision:  desired.Revision,
		Success:   true,
	}

	// applied tracks the identity of every object we successfully applied, so
	// prune can compute the complement (managed objects no longer desired).
	applied := map[objectKey]struct{}{}

	for _, m := range desired.Manifests {
		entry := protocol.ApplyResultEntry{Name: m.Name}
		keys, err := h.applyManifest(ctx, m)
		if err != nil {
			entry.Applied = false
			entry.Error = err.Error()
			status.Success = false
			h.log.Error("apply manifest", "name", m.Name, "namespace", m.Namespace, "error", err)
		} else {
			entry.Applied = true
			for _, k := range keys {
				applied[k] = struct{}{}
			}
		}
		status.Results = append(status.Results, entry)
	}

	pruned, err := h.prune(ctx, applied)
	if err != nil {
		// Prune failure does not fail the whole apply (objects were applied);
		// surface it on the status Error field but keep Success per-apply.
		h.log.Error("prune", "error", err)
		if status.Error == "" {
			status.Error = "prune: " + err.Error()
		}
	}
	status.Pruned = pruned

	return status
}

// objectKey identifies an applied object for prune diffing.
type objectKey struct {
	gvr       schema.GroupVersionResource
	namespace string
	name      string
}

// applyManifest parses one DesiredManifest (which may contain multiple YAML
// documents), validates each document's namespace is astronomer-owned, stamps
// the managed-by label, and server-side-applies it via Get→Create/Update.
// Returns the identity keys of every object applied (for prune tracking).
//
// SAFETY: a document whose namespace is empty (cluster-scoped) or NOT in
// AstronomerOwnedNamespaces is REFUSED — the whole manifest errors and nothing
// from it is applied past that point.
func (h *ReconcileHandler) applyManifest(ctx context.Context, m protocol.DesiredManifest) ([]objectKey, error) {
	docs := splitYAMLDocuments(m.Content)
	var keys []objectKey
	for i, doc := range docs {
		obj := &unstructured.Unstructured{}
		if err := k8syaml.Unmarshal([]byte(doc), &obj.Object); err != nil {
			return keys, fmt.Errorf("parse document %d: %w", i, err)
		}
		if len(obj.Object) == 0 {
			continue
		}

		gvk := obj.GroupVersionKind()
		if gvk.Kind == "" {
			return keys, fmt.Errorf("document %d: missing kind", i)
		}

		// Determine the target namespace: prefer the object's own metadata.namespace,
		// fall back to the manifest-level namespace hint.
		ns := obj.GetNamespace()
		if ns == "" {
			ns = m.Namespace
			obj.SetNamespace(ns)
		}

		// SAFETY BOUNDARY: refuse anything not in an astronomer-owned namespace.
		if !isOwnedNamespace(ns) {
			return keys, fmt.Errorf("document %d (%s/%s): namespace %q is not astronomer-owned; refusing to apply", i, gvk.Kind, obj.GetName(), ns)
		}

		gvr, namespaced, err := h.resolve(gvk)
		if err != nil {
			return keys, fmt.Errorf("document %d: resolve %s: %w", i, gvk.String(), err)
		}
		if !namespaced {
			// A cluster-scoped resource cannot be bounded to a namespace; refuse.
			return keys, fmt.Errorf("document %d (%s/%s): %s is cluster-scoped; pull reconcile applies namespaced resources only", i, gvk.Kind, obj.GetName(), gvk.Kind)
		}

		stampManagedBy(obj)

		if err := h.serverSideApply(ctx, gvr, obj); err != nil {
			return keys, fmt.Errorf("document %d (%s/%s): %w", i, gvk.Kind, obj.GetName(), err)
		}
		keys = append(keys, objectKey{gvr: gvr, namespace: ns, name: obj.GetName()})
	}
	return keys, nil
}

// serverSideApply applies obj idempotently via Get→Create/Update with conflict
// retry (the self_upgrade patchAgentDeployment pattern, generalized to the
// dynamic client). This is exercisable with the fake dynamic client, unlike the
// apply-patch path which the fake tracker does not merge.
func (h *ReconcileHandler) serverSideApply(ctx context.Context, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	ri := h.dyn.Resource(gvr).Namespace(obj.GetNamespace())
	name := obj.GetName()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := ri.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			_, cerr := ri.Create(ctx, obj, metav1.CreateOptions{FieldManager: pullFieldManager})
			return cerr
		}
		if err != nil {
			return err
		}
		next := obj.DeepCopy()
		next.SetResourceVersion(existing.GetResourceVersion())
		// Preserve cluster-assigned UID so the Update targets the same object.
		next.SetUID(existing.GetUID())
		_, uerr := ri.Update(ctx, next, metav1.UpdateOptions{FieldManager: pullFieldManager})
		return uerr
	})
}

// prune deletes managed-by-labeled objects in the astronomer-owned namespaces
// that are NOT in the desired (applied) set. It only ever lists/deletes within
// AstronomerOwnedNamespaces and only objects carrying the managed-by label, so
// unmanaged objects and objects outside the owned namespaces are never touched.
//
// It walks every (GVR, namespace) pair represented in the applied set — those
// are the resource kinds the agent manages — and prunes the stale complement.
// A kind we have never applied is left entirely alone.
func (h *ReconcileHandler) prune(ctx context.Context, applied map[objectKey]struct{}) (int, error) {
	// Collect the set of GVRs we manage (from the desired set). Pruning is
	// scoped to these kinds so we never list resource types we don't own.
	gvrs := map[schema.GroupVersionResource]struct{}{}
	for k := range applied {
		gvrs[k.gvr] = struct{}{}
	}

	selector := argolabels.ManagedByLabelKey + "=" + argolabels.ManagedByLabelValue
	var pruned int
	var firstErr error

	for gvr := range gvrs {
		for _, ns := range agenttemplate.AstronomerOwnedNamespaces {
			list, err := h.dyn.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for i := range list.Items {
				item := &list.Items[i]
				// Double safety: never prune outside owned namespaces and only
				// managed objects (the selector already enforces the label, but
				// re-check defensively).
				if !isOwnedNamespace(item.GetNamespace()) || !isManagedBy(item) {
					continue
				}
				key := objectKey{gvr: gvr, namespace: item.GetNamespace(), name: item.GetName()}
				if _, desired := applied[key]; desired {
					continue
				}
				if err := h.dyn.Resource(gvr).Namespace(item.GetNamespace()).Delete(ctx, item.GetName(), metav1.DeleteOptions{}); err != nil {
					if apierrors.IsNotFound(err) {
						continue
					}
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				pruned++
				h.log.Info("pruned managed object", "gvr", gvr.String(), "namespace", item.GetNamespace(), "name", item.GetName())
			}
		}
	}
	return pruned, firstErr
}

// isOwnedNamespace reports whether ns is one of the astronomer-owned namespaces
// the agent is permitted to apply into and prune within.
func isOwnedNamespace(ns string) bool {
	if ns == "" {
		return false
	}
	for _, owned := range agenttemplate.AstronomerOwnedNamespaces {
		if ns == owned {
			return true
		}
	}
	return false
}

// stampManagedBy sets the managed-by label on obj so prune can later identify
// it as an agent-managed object.
func stampManagedBy(obj *unstructured.Unstructured) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[argolabels.ManagedByLabelKey] = argolabels.ManagedByLabelValue
	obj.SetLabels(labels)
}

// isManagedBy reports whether obj carries the agent's managed-by label.
func isManagedBy(obj *unstructured.Unstructured) bool {
	return obj.GetLabels()[argolabels.ManagedByLabelKey] == argolabels.ManagedByLabelValue
}

// splitYAMLDocuments splits a multi-document YAML string into its constituent
// documents on the "---" separator, trimming empties.
func splitYAMLDocuments(content string) []string {
	raw := strings.Split(content, "\n---")
	out := make([]string, 0, len(raw))
	for _, d := range raw {
		d = strings.TrimSpace(strings.TrimPrefix(d, "---"))
		if d == "" {
			continue
		}
		out = append(out, d)
	}
	// Keep deterministic ordering as given (no sort) — but ensure stable empty
	// handling for single-doc inputs.
	if len(out) == 0 {
		return nil
	}
	return out
}
