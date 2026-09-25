package tunnel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const NamespaceCollectionQuery = "astronomerNamespace"

// SelectedCollectionNamespaces parses Astronomer's explicit namespace subset.
// An absent parameter preserves the native Kubernetes contract. Empty values
// never mean all namespaces. Only ordinary grouped API collection GETs qualify.
func SelectedCollectionNamespaces(r *http.Request) ([]string, bool, error) {
	values, present := r.URL.Query()[NamespaceCollectionQuery]
	if !present {
		return nil, false, nil
	}
	path, err := CanonicalK8sProxyPath(r)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if err != nil || r.Method != http.MethodGet || isWatchRequest(r) || len(parts) != 4 || parts[0] != "apis" || parts[3] == "namespaces" {
		return nil, true, errors.New("namespace subsets require an ordinary grouped resource collection GET")
	}
	if len(values) == 0 || len(values) > 100 {
		return nil, true, errors.New("select between 1 and 100 namespaces")
	}
	for _, value := range values {
		if len(validation.IsDNS1123Label(value)) > 0 {
			return nil, true, errors.New("namespace selection contains an invalid or empty name")
		}
	}
	slices.Sort(values)
	return slices.Compact(values), true, nil
}

type namespaceCursor struct {
	Binding  string `json:"b"`
	Index    int    `json:"i"`
	Continue string `json:"c,omitempty"`
	Expires  int64  `json:"e"`
}

type namespaceCollectionPage struct {
	names  []string
	cursor namespaceCursor
	limit  int
}

// prepareNamespaceCollection runs only on the agent-owning replica. Other
// replicas forward the original request so the owner's cursor key is stable.
func (p *ProxyHandler) prepareNamespaceCollection(r *http.Request) (*http.Request, *namespaceCollectionPage, int, error) {
	names, present, err := SelectedCollectionNamespaces(r)
	if err != nil {
		return nil, nil, http.StatusBadRequest, err
	}
	if !present {
		return r, nil, 0, nil
	}
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil || limit < 1 || limit > 500 {
		return nil, nil, http.StatusBadRequest, errors.New("limit must be between 1 and 500")
	}
	if allowed, scoped := namespaceFilterFromContext(r.Context()); scoped {
		for _, name := range names {
			if _, ok := allowed[name]; !ok {
				return nil, nil, http.StatusForbidden, errors.New("namespace is not authorized")
			}
		}
	}
	path, _ := CanonicalK8sProxyPath(r)
	q.Del("continue")
	q[NamespaceCollectionQuery] = names
	q.Set("limit", strconv.Itoa(limit))
	bindingBytes, _ := json.Marshal([]string{chi.URLParam(r, "cluster_id"), path, callerid.Resolve(r.Context()).User, q.Encode()})
	binding := sha256.Sum256(bindingBytes)
	page := &namespaceCollectionPage{names: names, limit: limit, cursor: namespaceCursor{Binding: base64.RawURLEncoding.EncodeToString(binding[:]), Expires: time.Now().Add(15 * time.Minute).Unix()}}
	if raw := r.URL.Query().Get("continue"); raw != "" {
		cursor, err := p.decodeNamespaceCursor(raw)
		if err != nil || cursor.Binding != page.cursor.Binding || cursor.Index < 0 || cursor.Index >= len(names) || cursor.Expires < time.Now().Unix() {
			return nil, nil, http.StatusGone, errors.New("namespace continuation expired or scope changed; restart the collection")
		}
		page.cursor = cursor
	}
	q.Del(NamespaceCollectionQuery)
	q.Del("continue")
	if page.cursor.Continue != "" {
		q.Set("continue", page.cursor.Continue)
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	path = "/" + strings.Join(parts[:3], "/") + "/namespaces/" + names[page.cursor.Index] + "/" + parts[3]
	request := r.Clone(r.Context())
	request.URL.RawQuery = q.Encode()
	request = WithCanonicalK8sProxyPath(request, path)
	// Explicitly request JSON: Table/protobuf responses cannot carry our paging envelope.
	request.Header.Set("Accept", "application/json")
	return request, page, 0, nil
}

func (p *ProxyHandler) decodeNamespaceCursor(raw string) (namespaceCursor, error) {
	var cursor namespaceCursor
	if len(raw) > 32768 {
		return cursor, errors.New("cursor too large")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return cursor, errors.New("invalid cursor")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, err
	}
	mac := hmac.New(sha256.New, p.namespaceCursorKey)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return cursor, errors.New("invalid cursor signature")
	}
	err = json.Unmarshal(body, &cursor)
	return cursor, err
}

func (p *ProxyHandler) encodeNamespaceCursor(cursor namespaceCursor) string {
	body, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, p.namespaceCursorKey)
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (p *ProxyHandler) finishNamespaceCollection(resp *protocol.K8sResponsePayload, page *namespaceCollectionPage) error {
	if page == nil || (resp.StatusCode != 0 && (resp.StatusCode < 200 || resp.StatusCode >= 300)) {
		return nil
	}
	body, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		return err
	}
	var list map[string]json.RawMessage
	if err := json.Unmarshal(body, &list); err != nil {
		return err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(list["items"], &items); err != nil || items == nil {
		return errors.New("invalid collection items")
	}
	if len(items) > page.limit {
		return errors.New("upstream exceeded namespace page limit")
	}
	for _, item := range items {
		var object struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if json.Unmarshal(item, &object) != nil || object.Metadata.Namespace != page.names[page.cursor.Index] {
			return errors.New("upstream returned an object outside the selected namespace")
		}
	}
	metadata := map[string]json.RawMessage{}
	if raw := list["metadata"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
			return errors.New("invalid collection metadata")
		}
	}
	var next string
	if raw := metadata["continue"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
	}
	page.cursor.Continue = next
	if next == "" {
		page.cursor.Index++
	}
	next = ""
	if page.cursor.Index < len(page.names) {
		next = p.encodeNamespaceCursor(page.cursor)
	}
	metadata["continue"], _ = json.Marshal(next)
	// These are namespace-local observations, not aggregate totals/snapshots.
	delete(metadata, "remainingItemCount")
	delete(metadata, "resourceVersion")
	delete(metadata, "selfLink")
	list["metadata"], _ = json.Marshal(metadata)
	body, err = json.Marshal(list)
	if err != nil {
		return fmt.Errorf("encode collection: %w", err)
	}
	resp.Body = base64.StdEncoding.EncodeToString(body)
	if resp.Headers == nil {
		resp.Headers = map[string]string{}
	}
	resp.Headers["Content-Type"] = "application/json"
	setContentLengthHeader(resp.Headers, len(body))
	return nil
}
