package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestNamespaceCollectionProxyPagination(t *testing.T) {
	hub := NewHub(nil)
	agent := &AgentConnection{ClusterID: "cluster", Streams: NewStreamManager(256), sendCh: make(chan *protocol.Message, sendChannelSize), cancel: func() {}}
	hub.agents.Set("cluster", agent)
	proxy := NewProxyHandler(hub, nil)
	router := chi.NewRouter()
	router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)
	user := uuid.New()
	paths := []string{}
	replies := []string{
		`{"kind":"WidgetList","metadata":{"continue":"native-a","remainingItemCount":51,"resourceVersion":"123"},"items":[{"metadata":{"namespace":"a","name":"first"}}]}`,
		`{"kind":"WidgetList","metadata":{},"items":[]}`,
		`{"kind":"WidgetList","metadata":{},"items":[{"metadata":{"namespace":"b","name":"last"}}]}`,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, body := range replies {
			select {
			case msg := <-agent.sendCh:
				var payload protocol.K8sRequestPayload
				_ = json.Unmarshal(msg.Payload, &payload)
				paths = append(paths, payload.Path)
				encoded, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(body))})
				stream, _ := agent.Streams.GetStream(msg.StreamID)
				stream.DataCh <- encoded
			case <-time.After(10 * time.Second):
				return
			}
		}
	}()
	cursor := ""
	for i := 0; i < 3; i++ {
		path := "/api/v1/clusters/cluster/k8s/apis/example.com/v1/widgets?astronomerNamespace=b&astronomerNamespace=a&limit=1"
		if cursor != "" {
			path += "&continue=" + url.QueryEscape(cursor)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(callerid.WithUser(context.Background(), user))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("page%d status%d: %s", i, rec.Code, rec.Body.String())
		}
		var list struct {
			Metadata map[string]json.RawMessage `json:"metadata"`
			Items    []json.RawMessage          `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"remainingItemCount", "resourceVersion"} {
			if _, ok := list.Metadata[key]; ok {
				t.Fatalf("misleading aggregate %s", key)
			}
		}
		_ = json.Unmarshal(list.Metadata["continue"], &cursor)
		if (cursor != "") != (i < 2) {
			t.Fatalf("page%d unexpected continuation %q", i, cursor)
		}
		if i == 1 && len(list.Items) != 0 {
			t.Fatal("expected empty page with continuation")
		}
	}
	<-done
	if len(paths) != 3 {
		t.Fatalf("wanted exactly one upstream call per page, got%v", paths)
	}
	for i, path := range paths {
		ns := "a"
		if i == 2 {
			ns = "b"
		}
		if !strings.HasPrefix(path, "/apis/example.com/v1/namespaces/"+ns+"/widgets?") || strings.Contains(path, NamespaceCollectionQuery) {
			t.Fatalf("wrong native request %s", path)
		}
		if (strings.Contains(path, "continue=native-a")) != (i == 1) {
			t.Fatalf("native cursor not correctly scoped: %s", path)
		}
	}
}

func TestNamespaceCollectionCursorScopeAndTampering(t *testing.T) {
	p := NewProxyHandler(NewHub(nil), nil)
	uid := uuid.New()
	request := func(path string, user uuid.UUID) *http.Request {
		r := httptest.NewRequest("GET", path, nil)
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("cluster_id", "cluster")
		return r.WithContext(callerid.WithUser(context.WithValue(r.Context(), chi.RouteCtxKey, ctx), user))
	}
	base := "/api/v1/clusters/cluster/k8s/apis/example.com/v1/widgets?astronomerNamespace=a&astronomerNamespace=b&limit=1"
	_, page, _, err := p.prepareNamespaceCollection(request(base, uid))
	if err != nil {
		t.Fatal(err)
	}
	token := p.encodeNamespaceCursor(page.cursor)
	tests := []struct {
		name, path, token string
		user              uuid.UUID
	}{
		{"valid", base, token, uid},
		{"different namespace", strings.Replace(base, "Namespace=b", "Namespace=c", 1), token, uid},
		{"different resource", strings.Replace(base, "widgets?", "secrets?", 1), token, uid},
		{"different user", base, token, uuid.New()},
		{"different selector", base + "&labelSelector=other", token, uid},
		{"different limit", strings.Replace(base, "limit=1", "limit=2", 1), token, uid},
		{"forged", base, "e30." + strings.Split(token, ".")[1], uid},
	}
	expired := page.cursor
	expired.Expires = time.Now().Add(-time.Hour).Unix()
	tests = append(tests, struct {
		name, path, token string
		user              uuid.UUID
	}{"expired", base, p.encodeNamespaceCursor(expired), uid})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, status, err := p.prepareNamespaceCollection(request(tt.path+"&continue="+url.QueryEscape(tt.token), tt.user))
			if tt.name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if status != 410 {
				t.Fatalf("status%d err%v", status, err)
			}
		})
	}
	otherCluster := request(base+"&continue="+url.QueryEscape(token), uid)
	otherContext := chi.NewRouteContext()
	otherContext.URLParams.Add("cluster_id", "another-cluster")
	otherCluster = otherCluster.WithContext(context.WithValue(otherCluster.Context(), chi.RouteCtxKey, otherContext))
	if _, _, status, _ := p.prepareNamespaceCollection(otherCluster); status != http.StatusGone {
		t.Fatalf("cluster change should expire cursor, got %d", status)
	}
	r := request(base+"&continue="+url.QueryEscape(token), uid)
	r = r.WithContext(WithNamespaceFilter(r.Context(), map[string]struct{}{"a": {}}))
	if _, _, status, _ := p.prepareNamespaceCollection(r); status != 403 {
		t.Fatalf("revoked namespace should forbid continuation, got%d", status)
	}
	otherOwner := NewProxyHandler(NewHub(nil), nil)
	if _, _, status, _ := otherOwner.prepareNamespaceCollection(request(base+"&continue="+url.QueryEscape(token), uid)); status != 410 {
		t.Fatalf("owner change should expire cursor, got%d", status)
	}
}

func TestNamespaceCollectionRejectsInvalidSelection(t *testing.T) {
	for _, suffix := range []string{"apis/g/v1/widgets?astronomerNamespace=", "apis/g/v1/widgets?astronomerNamespace=../other", "apis/g/v1/widgets?astronomerNamespace=a&watch=TRUE", "apis/g/v1/widgets/name?astronomerNamespace=a", "apis/g/v1/namespaces/a/widgets?astronomerNamespace=b", "apis/g/v1?astronomerNamespace=a"} {
		r := httptest.NewRequest("GET", "/api/v1/clusters/c/k8s/"+suffix, nil)
		if _, present, err := SelectedCollectionNamespaces(r); !present || err == nil {
			t.Fatalf("accepted invalid request%s", suffix)
		}
	}
	r := httptest.NewRequest("DELETE", "/api/v1/clusters/c/k8s/apis/g/v1/widgets?astronomerNamespace=a", nil)
	if _, _, err := SelectedCollectionNamespaces(r); err == nil {
		t.Fatal("accepted mutation")
	}
}

func TestNamespaceCollectionRejectsUnscopedUpstreamItems(t *testing.T) {
	p := NewProxyHandler(NewHub(nil), nil)
	page := &namespaceCollectionPage{names: []string{"a"}, limit: 1}
	for _, body := range []string{`{"items":[{"metadata":{"namespace":"b"}}]}`, `{"items":[{"metadata":{"name":"cluster-resource"}}]}`, `{"items":[],"metadata":null}`, `{"kind":"Table","rows":[]}`} {
		response := &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(body))}
		if err := p.finishNamespaceCollection(response, page); err == nil {
			t.Fatalf("accepted unsafe response%s", body)
		}
	}
}

func TestNamespaceCollectionCursorSurvivesForwardingToOwner(t *testing.T) {
	ownerHub := NewHub(nil)
	agent := &AgentConnection{ClusterID: "cluster", Streams: NewStreamManager(256), sendCh: make(chan *protocol.Message, sendChannelSize), cancel: func() {}}
	ownerHub.agents.Set("cluster", agent)
	owner := NewProxyHandler(ownerHub, nil)
	user := uuid.New()
	ownerRouter := chi.NewRouter()
	ownerRouter.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer caller" {
			http.Error(w, "missing caller authentication", http.StatusUnauthorized)
			return
		}
		owner.HandleK8sProxy(w, r.WithContext(callerid.WithUser(r.Context(), user)))
	})
	upstream := httptest.NewServer(ownerRouter)
	defer upstream.Close()
	oldClient := proxyHTTPClient
	proxyHTTPClient = upstream.Client()
	t.Cleanup(func() { proxyHTTPClient = oldClient })
	siblingHub := NewHub(nil)
	siblingHub.SetLocator(NewFakeLocatorForTest("self:8000", map[string]string{"cluster": strings.TrimPrefix(upstream.URL, "http://")}))
	sibling := NewProxyHandler(siblingHub, nil)
	siblingRouter := chi.NewRouter()
	siblingRouter.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", sibling.HandleK8sProxy)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			select {
			case msg := <-agent.sendCh:
				encoded, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(`{"kind":"WidgetList","metadata":{},"items":[]}`))})
				stream, _ := agent.Streams.GetStream(msg.StreamID)
				stream.DataCh <- encoded
			case <-time.After(10 * time.Second):
				return
			}
		}
	}()
	cursor := ""
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/v1/clusters/cluster/k8s/apis/g/v1/widgets?astronomerNamespace=a&astronomerNamespace=b&continue="+url.QueryEscape(cursor), nil)
		req.Header.Set("Authorization", "Bearer caller")
		rec := httptest.NewRecorder()
		siblingRouter.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("forwarded page%d status%d: %s", i, rec.Code, rec.Body.String())
		}
		var list struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		cursor = list.Metadata.Continue
		if (cursor != "") != (i == 0) {
			t.Fatalf("page%d continuation%q", i, cursor)
		}
	}
	<-done
}
