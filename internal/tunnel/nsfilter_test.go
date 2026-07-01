package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func allowSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// itemNamespaces extracts metadata.namespace (or metadata.name for a
// NamespaceList) from every item of a marshaled list body, for assertions.
func itemNamespaces(t *testing.T, body []byte) []string {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("unmarshal filtered body: %v", err)
	}
	kind, _ := obj["kind"].(string)
	items, _ := obj["items"].([]any)
	out := []string{}
	for _, raw := range items {
		m := raw.(map[string]any)
		meta := m["metadata"].(map[string]any)
		key := "namespace"
		if kind == "NamespaceList" {
			key = "name"
		}
		out = append(out, meta[key].(string))
	}
	return out
}

func TestFilterK8sListByNamespace_FiltersToAllowedNamespaces(t *testing.T) {
	body := []byte(`{
		"apiVersion":"v1",
		"kind":"PodList",
		"metadata":{"resourceVersion":"42"},
		"items":[
			{"metadata":{"name":"a","namespace":"team-a"}},
			{"metadata":{"name":"b","namespace":"team-b"}},
			{"metadata":{"name":"c","namespace":"team-a"}},
			{"metadata":{"name":"d","namespace":"forbidden"}}
		]
	}`)

	out, err := filterK8sListByNamespace(body, allowSet("team-a", "team-b"))
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	got := itemNamespaces(t, out)
	// team-a x2, team-b x1 kept; forbidden dropped.
	want := map[string]int{"team-a": 2, "team-b": 1}
	counts := map[string]int{}
	for _, ns := range got {
		counts[ns]++
	}
	for ns, n := range want {
		if counts[ns] != n {
			t.Fatalf("namespace %q count = %d, want %d (got %v)", ns, counts[ns], n, got)
		}
	}
	if counts["forbidden"] != 0 {
		t.Fatalf("forbidden namespace leaked: %v", got)
	}

	// Top-level metadata is preserved.
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	if obj["kind"] != "PodList" || obj["apiVersion"] != "v1" {
		t.Fatalf("kind/apiVersion not preserved: %v", obj)
	}
}

func TestFilterK8sListByNamespace_NamespaceListFiltersByName(t *testing.T) {
	body := []byte(`{
		"apiVersion":"v1",
		"kind":"NamespaceList",
		"items":[
			{"metadata":{"name":"team-a"}},
			{"metadata":{"name":"team-b"}},
			{"metadata":{"name":"kube-system"}}
		]
	}`)

	out, err := filterK8sListByNamespace(body, allowSet("team-a"))
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	got := itemNamespaces(t, out)
	if len(got) != 1 || got[0] != "team-a" {
		t.Fatalf("NamespaceList filter = %v, want [team-a]", got)
	}
}

func TestFilterK8sListByNamespace_ClusterScopedListBecomesEmpty(t *testing.T) {
	// NodeList items have no metadata.namespace → all dropped (fail closed).
	body := []byte(`{
		"apiVersion":"v1",
		"kind":"NodeList",
		"items":[
			{"metadata":{"name":"node-1"}},
			{"metadata":{"name":"node-2"}}
		]
	}`)

	out, err := filterK8sListByNamespace(body, allowSet("team-a"))
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	if got := itemNamespaces(t, out); len(got) != 0 {
		t.Fatalf("NodeList should filter to empty, got %v", got)
	}
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	if obj["kind"] != "NodeList" {
		t.Fatalf("kind not preserved on empty list: %v", obj)
	}
}

func TestFilterK8sListByNamespace_DropsItemsWithNoNamespaceKey(t *testing.T) {
	// A PodList where one item lacks a namespace must drop that item.
	body := []byte(`{
		"kind":"PodList",
		"items":[
			{"metadata":{"name":"a","namespace":"team-a"}},
			{"metadata":{"name":"weird"}}
		]
	}`)
	out, err := filterK8sListByNamespace(body, allowSet("team-a"))
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	if got := itemNamespaces(t, out); len(got) != 1 || got[0] != "team-a" {
		t.Fatalf("expected only team-a, got %v", got)
	}
}

func TestFilterK8sListByNamespace_TableFailsClosedToEmptyList(t *testing.T) {
	// A server-side Table (kubectl ?as=Table) is not a *List with items → empty.
	body := []byte(`{
		"kind":"Table",
		"apiVersion":"meta.k8s.io/v1",
		"columnDefinitions":[{"name":"Name"}],
		"rows":[{"cells":["secret-in-forbidden-ns"]}]
	}`)
	out, err := filterK8sListByNamespace(body, allowSet("team-a"))
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["kind"] != "Table" {
		t.Fatalf("kind not preserved: %v", obj)
	}
	items, _ := obj["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("Table must fail closed to empty items, got %v", items)
	}
	if _, ok := obj["rows"]; ok {
		t.Fatalf("Table rows must not leak through: %v", obj)
	}
}

func TestFilterK8sListByNamespace_NoKindFailsClosedWith403Signal(t *testing.T) {
	body := []byte(`{"items":[{"metadata":{"namespace":"forbidden"}}]}`)
	if _, err := filterK8sListByNamespace(body, allowSet("team-a")); err != errNamespaceFilterForbidden {
		t.Fatalf("expected errNamespaceFilterForbidden, got %v", err)
	}
}

func TestFilterK8sListByNamespace_InvalidJSONFailsClosed(t *testing.T) {
	if _, err := filterK8sListByNamespace([]byte("not json"), allowSet("team-a")); err != errNamespaceFilterForbidden {
		t.Fatalf("expected errNamespaceFilterForbidden, got %v", err)
	}
}

func TestApplyNamespaceFilter_RewritesBodyAndContentLength(t *testing.T) {
	list := `{"kind":"PodList","items":[` +
		`{"metadata":{"name":"a","namespace":"team-a"}},` +
		`{"metadata":{"name":"b","namespace":"forbidden"}}]}`
	resp := &protocol.K8sResponsePayload{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Length": "9999", "Content-Type": "application/json"},
		Body:       base64.StdEncoding.EncodeToString([]byte(list)),
	}

	if err := applyNamespaceFilter(resp, allowSet("team-a")); err != nil {
		t.Fatalf("applyNamespaceFilter: %v", err)
	}

	decoded, _ := base64.StdEncoding.DecodeString(resp.Body)
	got := itemNamespaces(t, decoded)
	if len(got) != 1 || got[0] != "team-a" {
		t.Fatalf("filtered items = %v, want [team-a]", got)
	}
	// Content-Length recomputed to match the new body.
	if resp.Headers["Content-Length"] != strconv.Itoa(len(decoded)) {
		t.Fatalf("content-length = %q, want %d", resp.Headers["Content-Length"], len(decoded))
	}
}

func TestApplyNamespaceFilter_NonTwoXXPassesThrough(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte(`{"kind":"Status","status":"Failure"}`))
	resp := &protocol.K8sResponsePayload{
		StatusCode: 404,
		Headers:    map[string]string{"Content-Length": "36"},
		Body:       raw,
	}
	if err := applyNamespaceFilter(resp, allowSet("team-a")); err != nil {
		t.Fatalf("non-2xx should pass through, got err %v", err)
	}
	if resp.Body != raw {
		t.Fatalf("non-2xx body was modified")
	}
	if resp.Headers["Content-Length"] != "36" {
		t.Fatalf("non-2xx content-length changed")
	}
}

func TestApplyNamespaceFilter_UnfilterableTwoXXFailsClosed(t *testing.T) {
	resp := &protocol.K8sResponsePayload{
		StatusCode: 200,
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"items":[{"metadata":{"namespace":"forbidden"}}]}`)),
	}
	if err := applyNamespaceFilter(resp, allowSet("team-a")); err != errNamespaceFilterForbidden {
		t.Fatalf("expected fail-closed error, got %v", err)
	}
}

func TestNamespaceFilterContextRoundTrip(t *testing.T) {
	if _, ok := namespaceFilterFromContext(context.Background()); ok {
		t.Fatal("bare context must not carry a filter")
	}
	want := allowSet("team-a", "team-b")
	ctx := WithNamespaceFilter(context.Background(), want)
	got, ok := namespaceFilterFromContext(ctx)
	if !ok || len(got) != 2 {
		t.Fatalf("round-trip failed: ok=%v got=%v", ok, got)
	}
}
