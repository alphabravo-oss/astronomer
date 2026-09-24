package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type pagedDiscoveryRequester struct {
	paths          []string
	identities     []protocol.CallerIdentity
	crdStatus      int
	malformed      bool
	builtinFailure bool
}

func discoveryTestCRD(name string) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": name + ".example.com", "annotations": map[string]any{"private": "do-not-return"}}, "spec": map[string]any{
		"group": "example.com", "scope": "Namespaced", "names": map[string]any{"plural": name, "kind": "Widget"},
		"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true, "schema": map[string]any{"private": "do-not-return"}}},
	}, "status": map[string]any{"private": "do-not-return"}}
}

func (f *pagedDiscoveryRequester) Do(ctx context.Context, _ string, method, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.paths = append(f.paths, path)
	f.identities = append(f.identities, callerid.Resolve(ctx))
	if method != http.MethodGet {
		return nil, fmt.Errorf("unexpected mutation")
	}
	uri, _ := url.Parse(path)
	status := http.StatusOK
	var payload map[string]any
	if strings.HasSuffix(uri.Path, "/customresourcedefinitions") {
		if f.crdStatus != 0 {
			status = f.crdStatus
			payload = map[string]any{"message": "upstream discovery failed"}
		} else if f.malformed {
			payload = map[string]any{"items": "invalid"}
		} else {
			items := []any{}
			next := ""
			if uri.Query().Get("continue") == "" {
				for i := 0; i < 500; i++ {
					items = append(items, discoveryTestCRD(fmt.Sprintf("widgets-%03d", i)))
				}
				next = "native cursor+/="
			} else {
				items = append(items, discoveryTestCRD("later-resource"))
			}
			payload = map[string]any{"metadata": map[string]any{"continue": next}, "items": items}
		}
	} else {
		if f.builtinFailure {
			status = 503
			payload = map[string]any{"message": "builtin discovery unavailable"}
		} else {
			resources := []any{}
			for _, resourceType := range enterpriseResourceMatrix {
				def := resourceDefs[resourceType]
				if def.apiBase == path {
					resources = append(resources, map[string]any{"name": def.plural, "kind": resourceType, "namespaced": def.namespaced, "verbs": []any{"get", "list"}})
				}
			}
			payload = map[string]any{"resources": resources}
		}
	}
	body, _ := json.Marshal(payload)
	return &protocol.K8sResponsePayload{StatusCode: status, Body: base64.StdEncoding.EncodeToString(body)}, nil
}

func discoveryRequest(t *testing.T, h *ResourceHandler, query string, user uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/resources/discovery/?"+query, nil)
	route := chi.NewRouteContext()
	route.URLParams.Add("cluster_id", uuid.NewString())
	req = req.WithContext(callerid.WithUser(context.WithValue(req.Context(), chi.RouteCtxKey, route), user))
	rec := httptest.NewRecorder()
	h.GetResourceDiscovery(rec, req)
	return rec
}

type discoveryTestEnvelope struct {
	Data struct {
		Resources []resourceDiscoveryEntry `json:"resources"`
		CRDs      []map[string]any         `json:"crds"`
		Continue  string                   `json:"crd_continue"`
		Partial   bool                     `json:"partial"`
		Errors    map[string]string        `json:"errors"`
	} `json:"data"`
}

func TestGetResourceDiscoveryContinuesBeyond500Definitions(t *testing.T) {
	requester := &pagedDiscoveryRequester{}
	h := NewResourceHandlerWithRequester(requester)
	user := uuid.New()
	first := discoveryRequest(t, h, "", user)
	if first.Code != 200 {
		t.Fatalf("first status%d %s", first.Code, first.Body.String())
	}
	var page discoveryTestEnvelope
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.CRDs) != 500 || page.Data.Continue != "native cursor+/=" || page.Data.Partial || len(page.Data.Resources) != len(enterpriseResourceMatrix) {
		t.Fatalf("unexpected first page: crds%d continue%q partial%v resources%d errors%v", len(page.Data.CRDs), page.Data.Continue, page.Data.Partial, len(page.Data.Resources), page.Data.Errors)
	}
	calls := len(requester.paths)
	seen := map[string]bool{}
	for _, path := range requester.paths {
		if seen[path] {
			t.Fatalf("duplicate builtin API request %s", path)
		}
		seen[path] = true
	}
	second := discoveryRequest(t, h, "crd_limit=500&crd_continue="+url.QueryEscape(page.Data.Continue), user)
	if second.Code != 200 {
		t.Fatalf("second status%d %s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.CRDs) != 1 || page.Data.CRDs[0]["plural"] != "later-resource" || page.Data.Continue != "" || page.Data.Partial || len(page.Data.Resources) != 0 {
		t.Fatalf("unexpected continuation page %+v", page.Data)
	}
	if len(requester.paths) != calls+1 {
		t.Fatalf("continuation repeated builtin discovery: %v", requester.paths[calls:])
	}
	parsed, _ := url.Parse(requester.paths[calls])
	if parsed.Query().Get("continue") != "native cursor+/=" {
		t.Fatalf("cursor was not forwarded opaquely: %s", requester.paths[calls])
	}
	if strings.Contains(first.Body.String(), "do-not-return") || strings.Contains(second.Body.String(), "do-not-return") {
		t.Fatal("summary leaked nonsummary fields")
	}
	for _, identity := range requester.identities {
		if identity.User != callerid.UserSubject(user) || identity.IsMachine() {
			t.Fatalf("caller identity changed: %+v", identity)
		}
	}
}

func TestGetResourceDiscoveryFailureAndBounds(t *testing.T) {
	user := uuid.New()
	for _, query := range []string{"crd_limit=0", "crd_limit=501", "crd_limit=oops", "crd_limit=", "crd_limit=1&crd_limit=2", "crd_continue=a&crd_continue=b", "crd_continue=" + strings.Repeat("x", 32769)} {
		requester := &pagedDiscoveryRequester{}
		rec := discoveryRequest(t, NewResourceHandlerWithRequester(requester), query, user)
		if rec.Code != 400 || len(requester.paths) != 0 {
			t.Fatalf("invalid query status%d made%d downstream calls", rec.Code, len(requester.paths))
		}
	}
	for _, tt := range []struct {
		name, query string
		fake        pagedDiscoveryRequester
		status      int
		partial     bool
	}{
		{"first CRD failure", "", pagedDiscoveryRequester{crdStatus: 503}, 200, true},
		{"first malformed CRD page", "", pagedDiscoveryRequester{malformed: true}, 200, true},
		{"builtin failure remains visible", "", pagedDiscoveryRequester{builtinFailure: true}, 200, true},
		{"expired continuation", "crd_continue=expired", pagedDiscoveryRequester{crdStatus: 410}, 410, false},
		{"denied continuation", "crd_continue=next", pagedDiscoveryRequester{crdStatus: 403}, 403, false},
		{"malformed continuation", "crd_continue=next", pagedDiscoveryRequester{malformed: true}, 502, false},
		{"upstream exceeds requested limit", "crd_limit=1", pagedDiscoveryRequester{}, 200, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := discoveryRequest(t, NewResourceHandlerWithRequester(&tt.fake), tt.query, user)
			if rec.Code != tt.status {
				t.Fatalf("status%d want%d: %s", rec.Code, tt.status, rec.Body.String())
			}
			if tt.partial {
				var page discoveryTestEnvelope
				_ = json.Unmarshal(rec.Body.Bytes(), &page)
				if !page.Data.Partial || len(page.Data.Errors) == 0 {
					t.Fatalf("error hidden: %s", rec.Body.String())
				}
			}
		})
	}
}
