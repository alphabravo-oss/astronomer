package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type resourceDrainCall struct {
	method string
	path   string
	body   []byte
}

type resourceDrainRequester struct {
	pods  drainPodList
	node  nodeActionResource
	calls []resourceDrainCall
}

func (r *resourceDrainRequester) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls = append(r.calls, resourceDrainCall{method: method, path: path, body: body})
	switch method + " " + path {
	case "GET /api/v1/pods?fieldSelector=spec.nodeName=node-1":
		return k8sJSONResponse(http.StatusOK, r.pods), nil
	case "GET /api/v1/nodes/node-1":
		return k8sJSONResponse(http.StatusOK, r.node), nil
	case "PATCH /api/v1/nodes/node-1":
		return k8sJSONResponse(http.StatusOK, map[string]any{"metadata": map[string]any{"name": "node-1"}}), nil
	case "POST /api/v1/namespaces/default/pods/app-0/eviction":
		return k8sJSONResponse(http.StatusCreated, map[string]any{"kind": "Eviction"}), nil
	default:
		return k8sJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}), nil
	}
}

func TestResourceHandlerDrainNodeEvictsEligiblePodsAndSkipsDaemonSets(t *testing.T) {
	requester := &resourceDrainRequester{pods: drainPodList{Items: []drainPod{
		testDrainPod("default", "app-0", "", false),
		testDrainPod("kube-system", "node-agent", "DaemonSet", false),
	}}}
	h := NewResourceHandlerWithRequester(requester)

	req := resourceRouteRequest(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/drain/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	})
	rr := httptest.NewRecorder()
	h.DrainNode(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data drainNodeResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Status != "drained" || len(envelope.Data.Evicted) != 1 || len(envelope.Data.Skipped) != 1 {
		t.Fatalf("drain response = %+v", envelope.Data)
	}
	if !resourceDrainSaw(requester.calls, http.MethodPost, "/api/v1/namespaces/default/pods/app-0/eviction") {
		t.Fatalf("eviction call missing: %+v", requester.calls)
	}
	if resourceDrainSaw(requester.calls, http.MethodPost, "/api/v1/namespaces/kube-system/pods/node-agent/eviction") {
		t.Fatalf("daemonset pod should not be evicted: %+v", requester.calls)
	}
}

func TestResourceHandlerDrainNodeBlocksEmptyDirPodsWithoutOverride(t *testing.T) {
	requester := &resourceDrainRequester{pods: drainPodList{Items: []drainPod{
		testDrainPod("default", "app-0", "", true),
	}}}
	h := NewResourceHandlerWithRequester(requester)

	req := resourceRouteRequest(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/drain/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	})
	rr := httptest.NewRecorder()
	h.DrainNode(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var envelope struct {
		Data drainNodeResponse `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.Status != "blocked" || len(envelope.Data.Blockers) != 1 {
		t.Fatalf("drain response = %+v", envelope.Data)
	}
	if resourceDrainSaw(requester.calls, http.MethodPost, "/api/v1/namespaces/default/pods/app-0/eviction") {
		t.Fatalf("blocked pod should not be evicted: %+v", requester.calls)
	}
	if !resourceDrainSaw(requester.calls, http.MethodPatch, "/api/v1/nodes/node-1") {
		t.Fatalf("node should still be cordoned before reporting blockers: %+v", requester.calls)
	}
}

func TestResourceHandlerSetAndRemoveNodeMetadata(t *testing.T) {
	requester := &resourceDrainRequester{node: testNodeActionResource()}
	h := NewResourceHandlerWithRequester(requester)

	req := resourceRouteRequestWithBody(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/labels/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	}, `{"key":"env","value":"prod"}`)
	rr := httptest.NewRecorder()
	h.SetNodeLabel(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("set label status = %d body=%s", rr.Code, rr.Body.String())
	}
	patch := resourceDrainLastPatch(t, requester.calls)
	labels := patch["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["env"] != "prod" {
		t.Fatalf("label patch = %#v", patch)
	}

	requester = &resourceDrainRequester{node: testNodeActionResource()}
	h = NewResourceHandlerWithRequester(requester)
	req = resourceRouteRequestWithBody(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/annotations/remove/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	}, `{"key":"remove.me/example"}`)
	rr = httptest.NewRecorder()
	h.RemoveNodeAnnotation(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("remove annotation status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !resourceDrainSaw(requester.calls, http.MethodGet, "/api/v1/nodes/node-1") {
		t.Fatalf("expected node GET before annotation removal: %+v", requester.calls)
	}
	patch = resourceDrainLastPatch(t, requester.calls)
	annotations := patch["metadata"].(map[string]any)["annotations"].(map[string]any)
	if _, ok := annotations["remove.me/example"]; ok {
		t.Fatalf("annotation removal patch still contains removed key: %#v", patch)
	}
	if annotations["keep"] != "yes" {
		t.Fatalf("annotation removal should preserve other keys: %#v", patch)
	}
}

func TestResourceHandlerAddAndRemoveNodeTaint(t *testing.T) {
	requester := &resourceDrainRequester{node: testNodeActionResource()}
	h := NewResourceHandlerWithRequester(requester)

	req := resourceRouteRequestWithBody(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/taints/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	}, `{"key":"gpu","value":"true","effect":"NoSchedule"}`)
	rr := httptest.NewRecorder()
	h.AddNodeTaint(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("add taint status = %d body=%s", rr.Code, rr.Body.String())
	}
	patch := resourceDrainLastPatch(t, requester.calls)
	taints := patch["spec"].(map[string]any)["taints"].([]any)
	if len(taints) != 2 {
		t.Fatalf("expected existing plus new taint, got %#v", taints)
	}
	added := taints[1].(map[string]any)
	if added["key"] != "gpu" || added["effect"] != "NoSchedule" {
		t.Fatalf("taint add patch = %#v", patch)
	}

	requester = &resourceDrainRequester{node: testNodeActionResource()}
	h = NewResourceHandlerWithRequester(requester)
	req = resourceRouteRequestWithBody(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/taints/remove/", map[string]string{
		"cluster_id": "cluster-1",
		"node_name":  "node-1",
	}, `{"key":"dedicated","effect":"NoSchedule"}`)
	rr = httptest.NewRecorder()
	h.RemoveNodeTaint(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("remove taint status = %d body=%s", rr.Code, rr.Body.String())
	}
	patch = resourceDrainLastPatch(t, requester.calls)
	if patch["spec"].(map[string]any)["taints"] != nil {
		t.Fatalf("last taint removal should clear taints with null patch: %#v", patch)
	}
}

func testDrainPod(namespace, name, ownerKind string, emptyDir bool) drainPod {
	var pod drainPod
	pod.Metadata.Namespace = namespace
	pod.Metadata.Name = name
	pod.Status.Phase = "Running"
	if ownerKind != "" {
		pod.Metadata.OwnerReferences = append(pod.Metadata.OwnerReferences, struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}{Kind: ownerKind, Name: name + "-owner"})
	}
	if emptyDir {
		pod.Spec.Volumes = append(pod.Spec.Volumes, struct {
			Name     string         `json:"name"`
			EmptyDir map[string]any `json:"emptyDir,omitempty"`
		}{Name: "scratch", EmptyDir: map[string]any{"medium": ""}})
	}
	return pod
}

func testNodeActionResource() nodeActionResource {
	var node nodeActionResource
	node.Metadata.Labels = map[string]string{"existing": "true"}
	node.Metadata.Annotations = map[string]string{"keep": "yes", "remove.me/example": "drop"}
	node.Spec.Taints = []nodeTaintRequest{{Key: "dedicated", Value: "batch", Effect: "NoSchedule"}}
	return node
}

func resourceRouteRequest(method, target string, params map[string]string) *http.Request {
	return resourceRouteRequestWithBody(method, target, params, "")
}

func resourceRouteRequestWithBody(method, target string, params map[string]string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func resourceDrainSaw(calls []resourceDrainCall, method, path string) bool {
	for _, call := range calls {
		if call.method == method && call.path == path {
			return true
		}
	}
	return false
}

func resourceDrainLastPatch(t *testing.T, calls []resourceDrainCall) map[string]any {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].method != http.MethodPatch {
			continue
		}
		var out map[string]any
		if err := json.Unmarshal(calls[i].body, &out); err != nil {
			t.Fatalf("decode patch body: %v", err)
		}
		return out
	}
	t.Fatalf("no patch call found: %+v", calls)
	return nil
}

func k8sJSONResponse(status int, payload any) *protocol.K8sResponsePayload {
	body, _ := json.Marshal(payload)
	return &protocol.K8sResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       base64.StdEncoding.EncodeToString(body),
	}
}
