package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const supportContinuationToken = "opaque+next/page="

func supportPagePeer(t *testing.T, resource string, expire bool) (*kubernetes.Clientset, func(string) int) {
	t.Helper()
	calls := map[string]int{}
	var mu sync.Mutex
	cert := testTLSCertPEM(t, "support.example.test")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, version := "", "v1"
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			kind = "Event"
		case strings.HasSuffix(r.URL.Path, "/networkpolicies"):
			kind, version = "NetworkPolicy", "networking.k8s.io/v1"
		case strings.HasSuffix(r.URL.Path, "/ingresses"):
			kind, version = "Ingress", "networking.k8s.io/v1"
		case strings.HasSuffix(r.URL.Path, "/secrets"):
			kind = "Secret"
		default:
			t.Errorf("unexpected Kubernetes path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		calls[kind]++
		mu.Unlock()
		if r.URL.Query().Get("limit") != "500" {
			t.Errorf("unbounded %s list limit %q", kind, r.URL.Query().Get("limit"))
		}
		if r.URL.Query().Get("resourceVersion") != "" {
			t.Error("snapshot resourceVersion was incorrectly added to a continued list request")
		}
		token := r.URL.Query().Get("continue")
		if token != "" && token != supportContinuationToken {
			t.Errorf("opaque continuation changed: %q", token)
		}
		w.Header().Set("Content-Type", "application/json")
		if kind == resource && token != "" && expire {
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Expired","code":410,"message":"fixture expired"}`))
			return
		}
		count, start, continuation := 1, 0, ""
		if kind == resource {
			if token == "" {
				count, continuation = 500, supportContinuationToken
			} else {
				start = 500
			}
		}
		items := make([]map[string]any, 0, count)
		for i := start; i < start+count; i++ {
			item := map[string]any{"metadata": map[string]any{"name": fmt.Sprintf("fixture-%d", i), "namespace": "management"}}
			if kind == "Event" {
				item["involvedObject"] = map[string]any{"kind": "Pod", "name": fmt.Sprintf("fixture-%d", i)}
			}
			if kind == "Ingress" {
				item["spec"] = map[string]any{"tls": []any{map[string]any{"secretName": "fixture-500"}}}
			}
			if kind == "Secret" && (i == 500 || resource != "Secret") {
				item["metadata"] = map[string]any{"name": "fixture-500", "namespace": "management"}
				item["type"] = "kubernetes.io/tls"
				item["data"] = map[string][]byte{"tls.crt": cert, "tls.key": []byte("private-key-must-stay-redacted")}
			}
			items = append(items, item)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"apiVersion": version, "kind": kind + "List", "metadata": map[string]any{"resourceVersion": "101", "continue": continuation}, "items": items}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(peer.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: peer.URL})
	if err != nil {
		t.Fatal(err)
	}
	return client, func(kind string) int {
		mu.Lock()
		defer mu.Unlock()
		return calls[kind]
	}
}

func supportContinuationSection(t *testing.T, client kubernetes.Interface, kind string) (map[string][]byte, []string) {
	t.Helper()
	h := NewSupportBundleHandler(nil, nil, client, "management")
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	log := newSectionLog()
	switch kind {
	case "Event":
		h.writeEvents(context.Background(), archive, log)
	case "NetworkPolicy":
		h.writeNetworkPolicies(context.Background(), archive, log)
	default:
		h.writeIngressCertificates(context.Background(), archive, log)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return readZipFiles(t, buffer.Bytes()), log.lines
}

func TestSupportBundleKubernetesContinuesNamespaceLists(t *testing.T) {
	for _, kind := range []string{"Event", "NetworkPolicy", "Ingress"} {
		t.Run(kind, func(t *testing.T) {
			client, calls := supportPagePeer(t, kind, false)
			files, sections := supportContinuationSection(t, client, kind)
			name, identity := "events.json", "object_name"
			if kind == "NetworkPolicy" {
				name, identity = "networkpolicies.json", "name"
			}
			if kind == "Ingress" {
				name, identity = "ingress-certificates.json", "name"
			}
			var payload any
			if err := json.Unmarshal(files[name], &payload); err != nil {
				t.Fatal(err)
			}
			if kind == "Ingress" {
				payload = payload.(map[string]any)["ingresses"]
			}
			items, ok := payload.([]any)
			if !ok || len(items) != 501 || calls(kind) != 2 {
				t.Fatalf("namespace snapshot omitted next page: items=%d, requests=%d, sections=%v", len(items), calls(kind), sections)
			}
			if items[500].(map[string]any)[identity] != "fixture-500" {
				t.Fatalf("next-page identity missing: %v", items[500])
			}
		})
	}
}

func TestSupportBundleTLSSecretBeyondFirstPageIsPresent(t *testing.T) {
	client, calls := supportPagePeer(t, "Secret", false)
	files, sections := supportContinuationSection(t, client, "Secret")
	data := files["ingress-certificates.json"]
	if calls("Secret") != 2 || !bytes.Contains(data, []byte(`"present": true`)) || bytes.Contains(data, []byte(`"present": false`)) {
		t.Fatalf("actual TLS secret reported missing: requests=%d, sections=%v, output=%s", calls("Secret"), sections, data)
	}
	if bytes.Contains(data, []byte("private-key-must-stay-redacted")) {
		t.Fatal("support snapshot exposed private key")
	}
}

func TestSupportBundleExpiredSecretContinuationNeverClaimsAbsent(t *testing.T) {
	client, _ := supportPagePeer(t, "Secret", true)
	files, sections := supportContinuationSection(t, client, "Secret")
	if _, ok := files["ingress-certificates.json"]; ok {
		t.Fatalf("incomplete secret snapshot emitted certificate absence projection: sections=%v", sections)
	}
	if !strings.Contains(strings.Join(sections, "\n"), "incomplete Kubernetes snapshot: continuation expired") {
		t.Fatalf("expired upstream snapshot not reported incomplete: %v", sections)
	}
}

func TestSupportBundleIncompleteNamespaceSnapshotIsExplicit(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		requests int
		category string
	}{
		{"cycle", 2, "continuation repeated"},
		{"changed-version", 2, "snapshot changed"},
		{"missing-version", 1, "snapshot identity missing"},
		{"page-budget", 10, "page budget exhausted"},
		{"oversized-page", 1, "page size exceeded"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				request := requests.Add(1)
				if r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("resourceVersion") != "" {
					t.Errorf("invalid list options: %s", r.URL.RawQuery)
				}
				version, continuation, count := "101", "same-token", 1
				switch scenario.name {
				case "changed-version":
					if request > 1 {
						version, continuation = "102", ""
					}
				case "missing-version":
					version = ""
				case "page-budget":
					continuation = fmt.Sprintf("page-%d", request)
				case "oversized-page":
					count, continuation = 501, ""
				}
				items := make([]map[string]any, count)
				for i := range items {
					items[i] = map[string]any{"message": "fixture event"}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{
					"apiVersion": "v1", "kind": "EventList", "items": items,
					"metadata": map[string]any{"resourceVersion": version, "continue": continuation},
				}); err != nil {
					t.Error(err)
				}
			}))
			defer peer.Close()
			client, err := kubernetes.NewForConfig(&rest.Config{Host: peer.URL})
			if err != nil {
				t.Fatal(err)
			}
			files, sections := supportContinuationSection(t, client, "Event")
			if _, ok := files["events.json"]; ok {
				t.Fatal("incomplete snapshot was emitted as a complete event list")
			}
			if int(requests.Load()) != scenario.requests || !strings.Contains(strings.Join(sections, "\n"), "incomplete Kubernetes snapshot: "+scenario.category) {
				t.Fatalf("incomplete state missing or unbounded requests: requests=%d sections=%v", requests.Load(), sections)
			}
		})
	}
}

func TestSupportBundleContinuationStopsOnCallerCancellation(t *testing.T) {
	secondStarted, secondStopped := make(chan struct{}), make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") != "" {
			close(secondStarted)
			<-r.Context().Done()
			close(secondStopped)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"apiVersion": "v1", "kind": "EventList", "items": []any{},
			"metadata": map[string]any{"resourceVersion": "101", "continue": supportContinuationToken},
		}); err != nil {
			t.Error(err)
		}
	}))
	defer peer.Close()
	client, err := kubernetes.NewForConfig(&rest.Config{Host: peer.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	log := newSectionLog()
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewSupportBundleHandler(nil, nil, client, "management").writeEvents(ctx, archive, log)
	}()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("continued request did not reach actual HTTP peer")
	}
	cancel()
	for _, stopped := range []<-chan struct{}{done, secondStopped} {
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatal("caller cancellation did not stop continued request and collector")
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := readZipFiles(t, buffer.Bytes())["events.json"]; ok {
		t.Fatal("cancelled partial snapshot emitted a complete event list")
	}
	if !strings.Contains(strings.Join(log.lines, "\n"), "incomplete Kubernetes snapshot: request stopped") {
		t.Fatalf("cancelled snapshot not reported incomplete: %v", log.lines)
	}
}
