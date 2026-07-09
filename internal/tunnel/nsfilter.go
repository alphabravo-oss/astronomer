package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// nsFilterContextKey is the private context key under which the k8s passthrough
// authz middleware stashes the namespace allow-set for a namespace-scoped user's
// cluster-wide LIST. Its presence is the sole signal that the unary response body
// MUST be filtered down to those namespaces before it reaches the client.
type nsFilterContextKey struct{}

// WithNamespaceFilter returns a child context carrying the namespace allow-set
// that the passthrough proxy must enforce on the (buffered) list response. The
// caller passes the exact set of namespaces the user may see for this
// (resource, list) at this cluster. An empty/nil map would filter everything to
// nothing, so callers must only stash a non-empty allow-set (the middleware
// already guards on len(names) > 0).
func WithNamespaceFilter(ctx context.Context, allowed map[string]struct{}) context.Context {
	return context.WithValue(ctx, nsFilterContextKey{}, allowed)
}

// namespaceFilterFromContext reports whether ctx carries a namespace allow-set
// and returns it. ok is true only when WithNamespaceFilter stashed a set — a
// request without the key returns (nil, false) and is served unfiltered, so the
// default (flag off, or a non-filtered path) is byte-identical to before.
func namespaceFilterFromContext(ctx context.Context) (map[string]struct{}, bool) {
	v, ok := ctx.Value(nsFilterContextKey{}).(map[string]struct{})
	return v, ok
}

// errNamespaceFilterForbidden signals that a 2xx response cannot be safely
// filtered for a namespace-scoped user (not a recognizable List, an unparseable
// body, or a List with no kind) and the caller must fail closed with a 403
// instead of forwarding potentially-unauthorized data.
var errNamespaceFilterForbidden = errors.New("tunnel: k8s list response not filterable for namespace-scoped user")

// applyNamespaceFilter rewrites resp in place so a namespace-scoped user only
// receives list items from their allow-set. It is a no-op for non-2xx responses
// (apiserver errors pass through unchanged). For a 2xx response it decodes the
// buffered body, drops every item outside the allow-set, and recomputes
// Content-Length. It returns errNamespaceFilterForbidden when the body cannot be
// filtered safely (fail closed).
func applyNamespaceFilter(resp *protocol.K8sResponsePayload, allowed map[string]struct{}) error {
	// StatusCode 0 is normalized to 200 by writeK8sResponse, so treat it as 2xx.
	if resp.StatusCode != 0 && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		return nil
	}

	var bodyBytes []byte
	if resp.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(resp.Body)
		if err != nil {
			// A 2xx body that isn't valid base64 on a filtered request is
			// unexpected and unfilterable — fail closed rather than risk
			// forwarding it raw.
			return errNamespaceFilterForbidden
		}
		bodyBytes = decoded
	}

	filtered, err := filterK8sListByNamespace(bodyBytes, allowed)
	if err != nil {
		return err
	}

	resp.Body = base64.StdEncoding.EncodeToString(filtered)
	if resp.Headers == nil {
		resp.Headers = map[string]string{}
	}
	setContentLengthHeader(resp.Headers, len(filtered))
	return nil
}

// filterK8sListByNamespace keeps only the list items whose namespace-key is in
// allowed. The namespace-key is metadata.namespace for ordinary namespaced
// resources, or metadata.name when the list kind is "NamespaceList" (so a
// cluster-wide `kubectl get ns` returns only the caller's namespaces, matching
// the typed ListNamespaces handler). Items with no namespace-key (cluster-scoped
// resources such as Node/PV, or malformed items) are DROPPED — a cluster-scoped
// list therefore collapses to an empty list for a namespace-scoped user.
//
// Any 2xx body that is not a recognizable `*List` with an items array fails
// closed: if the object carries a kind we return an empty-items List of that
// kind (so a Table or single object yields no data); if it has no kind, or is
// unparseable JSON, we return errNamespaceFilterForbidden so the caller emits a
// 403. This guarantees a namespace-scoped user can never receive data the filter
// does not understand.
func filterK8sListByNamespace(bodyBytes []byte, allowed map[string]struct{}) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(bodyBytes, &obj); err != nil {
		return nil, errNamespaceFilterForbidden
	}

	kind, _ := obj["kind"].(string)
	if kind == "" {
		return nil, errNamespaceFilterForbidden
	}

	items, hasItems := obj["items"].([]any)
	if !strings.HasSuffix(kind, "List") || !hasItems {
		// Not a recognizable List with items (Table, single object, or a List
		// whose items are null/absent) → fail closed to an empty list.
		return emptyListBody(obj, kind)
	}

	kept := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue // malformed item → drop (fail closed)
		}
		ns, ok := itemNamespaceKey(item, kind)
		if !ok {
			continue // no namespace-key → drop (cluster-scoped / malformed)
		}
		if _, allow := allowed[ns]; allow {
			kept = append(kept, raw)
		}
	}
	obj["items"] = kept
	return json.Marshal(obj)
}

// itemNamespaceKey extracts the namespace an item belongs to. For a
// NamespaceList the item IS a namespace, so its identity is metadata.name;
// otherwise it is metadata.namespace. Returns ok=false when the key is missing
// or empty, which callers treat as "drop".
func itemNamespaceKey(item map[string]any, kind string) (string, bool) {
	meta, ok := item["metadata"].(map[string]any)
	if !ok {
		return "", false
	}
	key := "namespace"
	if kind == "NamespaceList" {
		key = "name"
	}
	v, ok := meta[key].(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// emptyListBody returns a minimal, safe empty-items List that preserves the
// apiVersion/kind/metadata of the original object so clients still see a
// well-formed (but empty) list rather than authorized data.
func emptyListBody(obj map[string]any, kind string) ([]byte, error) {
	out := map[string]any{
		"kind":  kind,
		"items": []any{},
	}
	if av, ok := obj["apiVersion"].(string); ok {
		out["apiVersion"] = av
	}
	if md, ok := obj["metadata"]; ok {
		out["metadata"] = md
	} else {
		out["metadata"] = map[string]any{}
	}
	return json.Marshal(out)
}

// watchEventAllowed reports whether a Kubernetes watch event JSON payload
// (type + object) is in the namespace allow-set. Events for disallowed or
// unparseable namespaces are dropped (fail closed for data plane). Bookmark
// / ERROR events with no object namespace pass through when type is BOOKMARK
// or ERROR so clients can stay in sync.
func watchEventAllowed(body []byte, allowed map[string]struct{}) (bool, error) {
	var ev map[string]any
	if err := json.Unmarshal(body, &ev); err != nil {
		return false, err
	}
	typ, _ := ev["type"].(string)
	if typ == "BOOKMARK" || typ == "ERROR" {
		return true, nil
	}
	obj, ok := ev["object"].(map[string]any)
	if !ok {
		return false, nil
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return false, nil
	}
	// Namespace objects: identity is metadata.name.
	kind, _ := obj["kind"].(string)
	if kind == "Namespace" {
		name, _ := meta["name"].(string)
		if name == "" {
			return false, nil
		}
		_, ok := allowed[name]
		return ok, nil
	}
	ns, _ := meta["namespace"].(string)
	if ns == "" {
		// Cluster-scoped object on a watch — drop for ns-scoped callers.
		return false, nil
	}
	_, ok = allowed[ns]
	return ok, nil
}

// setContentLengthHeader replaces any existing (case-insensitive) Content-Length
// entry with the recomputed length so the filtered body advertises the correct
// size. Content-Length is on k8sProxyResponseHeaderAllowed, so it is forwarded.
func setContentLengthHeader(h map[string]string, n int) {
	for k := range h {
		if strings.EqualFold(k, "Content-Length") {
			delete(h, k)
		}
	}
	h["Content-Length"] = strconv.Itoa(n)
}
