package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// podFrameNamespace extracts metadata.namespace from a raw pod-watch object,
// returning "" when the object is absent, unparseable, or carries no namespace.
// Used to confine a namespace-scoped watcher to its authorized allow-set.
func podFrameNamespace(obj json.RawMessage) string {
	if len(obj) == 0 {
		return ""
	}
	var meta struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(obj, &meta); err != nil {
		return ""
	}
	return meta.Metadata.Namespace
}

// PodWatchEvent is one Kubernetes watch event for a pod: the watch verb
// (ADDED/MODIFIED/DELETED/BOOKMARK/ERROR) plus the raw pod object JSON exactly
// as the upstream API server emitted it. Object is left as a RawMessage so the
// handler forwards it to the browser without re-encoding (and without coupling
// to a pod struct that would drift from the k8s schema).
type PodWatchEvent struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}

// PodWatcher opens a live watch on pods for one cluster and streams decoded
// watch events on the returned channel until ctx is cancelled or the upstream
// watch closes (at which point the channel is closed). namespace "" watches
// all namespaces. Implemented in production by the tunnel-backed watcher; the
// SSE handler is tested against a fake.
type PodWatcher interface {
	WatchPods(ctx context.Context, clusterID, namespace string) (<-chan PodWatchEvent, error)
}

// SetPodWatcher wires the live pod watcher used by the WatchPods SSE endpoint.
// Optional; without it WatchPods returns 501.
func (h *WorkloadHandler) SetPodWatcher(w PodWatcher) {
	if h == nil {
		return
	}
	h.podWatcher = w
}

// WatchPods streams live pod add/update/delete events for a cluster over
// Server-Sent Events instead of the UI polling the list endpoint.
//
//	GET /api/v1/clusters/{cluster_id}/pods/watch/?namespace=<ns>&ticket=<t>
//
// Each k8s watch event becomes one SSE frame whose `event:` is the watch verb
// (ADDED/MODIFIED/DELETED) and whose `data:` is the pod object JSON:
//
//	es.addEventListener('ADDED',    e => upsert(JSON.parse(e.data)));
//	es.addEventListener('MODIFIED', e => upsert(JSON.parse(e.data)));
//	es.addEventListener('DELETED',  e => remove(JSON.parse(e.data)));
//
// Auth is enforced by the stream-ticket-or-auth middleware on the route (same
// posture as the pod-logs stream), so this handler only opens the watch.
func (h *WorkloadHandler) WatchPods(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	namespace := r.URL.Query().Get("namespace")
	if h == nil || h.podWatcher == nil {
		RespondRequestError(w, r, http.StatusNotImplemented, apierror.NotImplemented, "pod watch streaming not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "streaming unsupported")
		return
	}

	// F7: namespace-scoped tenants are admitted by the LIST route gate (which
	// only proves they hold pods:read in ≥1 namespace, or the specific namespace
	// they pinned). The upstream watch, however, is opened cluster-wide when no
	// namespace is pinned, so we must filter emitted frames down to the caller's
	// authorized namespaces before forwarding — the same gate+filter invariant
	// ListPods relies on. `all==true` (feature flag off, superuser, or a
	// cluster-wide grant) forwards everything unfiltered; `!all` is a strict
	// fail-closed allow-list.
	all, allowed, err := h.authz.authorizedNamespaces(r.Context(), parseClusterUUID(clusterID), rbac.ResourcePods, rbac.VerbRead)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}

	events, err := h.podWatcher.WatchPods(r.Context(), clusterID, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Keepalive comment keeps idle-closing proxies from dropping the watch.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == "" {
				continue
			}
			// Namespace-scoped caller: drop any frame whose pod is outside the
			// authorized allow-set (fail closed — a frame with a missing/empty
			// namespace is dropped, matching filterItemsByNamespaceKey). A frame
			// carrying no object (e.g. a bare ERROR) has no namespace to prove
			// ownership of, so it is also dropped for a restricted caller.
			if !all {
				ns := podFrameNamespace(ev.Object)
				if ns == "" {
					continue
				}
				if _, ok := allowed[ns]; !ok {
					continue
				}
			}
			// data is the pod object JSON; "null" when the watch frame
			// carried no object (e.g. ERROR/BOOKMARK without one).
			data := ev.Object
			if len(data) == 0 {
				data = json.RawMessage("null")
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
