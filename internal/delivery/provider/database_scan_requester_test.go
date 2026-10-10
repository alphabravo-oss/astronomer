package provider

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type ownedScanRecordingRequester struct {
	calls        int
	lastIdentity protocol.CallerIdentity
}

func (requester *ownedScanRecordingRequester) Do(ctx context.Context, cluster, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	requester.calls++
	requester.lastIdentity = callerid.Resolve(ctx)
	return &protocol.K8sResponsePayload{StatusCode: 200}, nil
}

func TestOwnedDatabaseScanOriginScopesActualRequestsAndPreservesForwarding(t *testing.T) {
	cluster := uuid.New()
	peer := &ownedScanRecordingRequester{}
	pod := "/api/v1/namespaces/tenant/pods/current-workload"
	node := "/api/v1/nodes/current-node"
	collection := "/api/v1/namespaces/astronomer-database-scans/secrets"
	payload := []byte(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"claim-secret","namespace":"astronomer-database-scans"}}`)
	writes := map[string]string{collection: digest.FromBytes(payload).String()}
	reads := []string{pod, node}
	scoped, err := newDatabaseScanScopedRequester(peer, cluster, reads, writes)
	if err != nil {
		t.Fatal(err)
	}
	machine := callerid.WithMachine(t.Context(), callerid.SourceDatabaseScan)
	for _, path := range reads {
		if _, err := scoped.Do(machine, cluster.String(), http.MethodGet, path, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scoped.Do(machine, cluster.String(), http.MethodPost, collection, payload, map[string]string{"Content-Type": "application/json"}); err != nil {
		t.Fatal(err)
	}
	if peer.calls != 3 || peer.lastIdentity.User != callerid.MachineSubject(callerid.SourceDatabaseScan) || !peer.lastIdentity.IsMachine() {
		t.Fatal("actual controller requests lack exact origin")
	}
	// Existing user attribution remains authoritative across a transport hop.
	user := protocol.CallerIdentity{User: callerid.UserSubject(uuid.New()), Origin: protocol.OriginUser}
	if !reflect.DeepEqual(callerid.Fill(callerid.WithMachine(machine, callerid.SourceCrossPodTransport), user), user) {
		t.Fatal("machine hop overwrote actual user forwarding identity")
	}
	// Scope ownership survives mutation of the private builder's source inputs.
	reads[0] = "/api/v1/namespaces/foreign/pods/arbitrary"
	writes[collection] = digest.FromString("changed").String()
	if _, err := scoped.Do(machine, cluster.String(), http.MethodGet, pod, nil, nil); err != nil {
		t.Fatal("captured read scope mutated")
	}
	if _, err := scoped.Do(machine, cluster.String(), http.MethodPost, collection, payload, map[string]string{"Content-Type": "application/json"}); err != nil {
		t.Fatal("captured write scope mutated")
	}
}

func TestOwnedDatabaseScanRequesterRejectsUserForeignClusterNamespaceAndWrites(t *testing.T) {
	cluster := uuid.New()
	peer := &ownedScanRecordingRequester{}
	path := "/api/v1/namespaces/tenant/pods/current-workload"
	payload := []byte(`{"owned":"exact"}`)
	ownedCollection := "/api/v1/namespaces/astronomer-database-scans/secrets"
	scoped, err := newDatabaseScanScopedRequester(peer, cluster, []string{path}, map[string]string{ownedCollection: digest.FromBytes(payload).String()})
	if err != nil {
		t.Fatal(err)
	}
	machine := callerid.WithMachine(t.Context(), callerid.SourceDatabaseScan)
	cases := []struct {
		name                  string
		ctx                   context.Context
		cluster, method, path string
		body                  []byte
		headers               map[string]string
	}{
		{"authenticated user", callerid.WithUser(t.Context(), uuid.New()), cluster.String(), "GET", path, nil, nil},
		{"user plus machine", callerid.WithMachine(callerid.WithUser(t.Context(), uuid.New()), callerid.SourceDatabaseScan), cluster.String(), "GET", path, nil, nil},
		{"no origin", t.Context(), cluster.String(), "GET", path, nil, nil},
		{"unrelated machine", callerid.WithMachine(t.Context(), callerid.SourceAgentLifecycle), cluster.String(), "GET", path, nil, nil},
		{"foreign cluster", machine, uuid.NewString(), "GET", path, nil, nil},
		{"foreign workload", machine, cluster.String(), "GET", "/api/v1/namespaces/foreign/pods/current-workload", nil, nil},
		{"arbitrary Node", machine, cluster.String(), "GET", "/api/v1/nodes/foreign-node", nil, nil},
		{"foreign namespace write", machine, cluster.String(), "POST", "/api/v1/namespaces/tenant/secrets", payload, map[string]string{"Content-Type": "application/json"}},
		{"workload write", machine, cluster.String(), "POST", path, payload, map[string]string{"Content-Type": "application/json"}},
		{"arbitrary object body", machine, cluster.String(), "POST", ownedCollection, []byte(`{"arbitrary":true}`), map[string]string{"Content-Type": "application/json"}},
		{"identity header", machine, cluster.String(), "POST", ownedCollection, payload, map[string]string{"Content-Type": "application/json", "Impersonate-User": "client"}},
		{"credential header", machine, cluster.String(), "GET", path, nil, map[string]string{"Authorization": "private"}},
		{"delete unsupported", machine, cluster.String(), "DELETE", ownedCollection, nil, nil},
		{"read body", machine, cluster.String(), "GET", path, payload, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := peer.calls
			if _, err := scoped.Do(test.ctx, test.cluster, test.method, test.path, test.body, test.headers); err == nil || peer.calls != before {
				t.Fatal("unowned request reached tunnel")
			}
		})
	}
}
