package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// identityBearingPayload is a payload shaped exactly as the Phase 0 server
// sends it: a populated typed identity AND a client's attempt to smuggle the
// same thing through headers.
func identityBearingPayload(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := json.Marshal(protocol.K8sRequestPayload{
		Method: http.MethodGet,
		Path:   path,
		Headers: map[string]string{
			"Accept":            "application/json",
			"Impersonate-User":  "system:admin",
			"Impersonate-Group": "system:masters",
			"X-Remote-User":     "system:admin",
			"X-Remote-Group":    "system:masters",
		},
		CallerIdentity: protocol.CallerIdentity{
			User:   protocol.UserSubjectPrefix + "11111111-1111-1111-1111-111111111111",
			Origin: protocol.OriginUser,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

// assertNoIdentityHeaders is the shared assertion for both proxy paths: nothing
// resembling an identity claim may reach the kube-apiserver.
func assertNoIdentityHeaders(t *testing.T, got http.Header) {
	t.Helper()
	for name := range got {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "impersonate-") || strings.HasPrefix(lower, "x-remote-") {
			t.Errorf("header %q reached the upstream apiserver", name)
		}
	}
}

// TestUpstreamRequestCarriesNoImpersonationWithFlagOff is THE Phase 0 safety
// assertion on the agent side.
//
// The payload carries a fully populated caller identity — that is the whole
// point of this phase — and the flag is at its `off` default. The agent must
// therefore make an upstream request that is byte-identical to today's: the
// identity is DROPPED, not translated into headers. It also must not let the
// client's own Impersonate-* / X-Remote-* headers through (§7 invariant 2).
//
// PRE-FIX BEHAVIOUR: the header-stripping half already held, via the proxyhdr
// allowlist in executeUpstream and HandleStreamRequest. What did not exist was
// the identity on the payload, so "the agent must ignore it" was not a property
// anything could regress. It is now, and this test is what pins it.
func TestUpstreamRequestCarriesNoImpersonationWithFlagOff(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: server.URL},
		httpClient: server.Client(),
		log:        slog.Default(),
	}
	res, err := proxy.executeUpstream(context.Background(), &protocol.Message{
		Payload: identityBearingPayload(t, "/api/v1/pods"),
	})
	if err != nil {
		t.Fatalf("executeUpstream: %v", err)
	}
	defer res.Release()

	assertNoIdentityHeaders(t, <-seen)
}

// TestStreamUpstreamRequestCarriesNoImpersonationWithFlagOff is the same
// assertion for the watch/streaming path, which builds its own http.Request and
// therefore has its own copy of the header loop. Pinning only executeUpstream
// would leave half the proxy surface unguarded.
func TestStreamUpstreamRequestCarriesNoImpersonationWithFlagOff(t *testing.T) {
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":"ADDED","object":{}}` + "\n"))
	}))
	defer server.Close()

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: server.URL},
		httpClient: server.Client(),
		log:        slog.Default(),
		streams:    make(map[string]context.CancelFunc),
	}
	err := proxy.HandleStreamRequest(context.Background(), &protocol.Message{
		StreamID: "s1",
		Payload:  identityBearingPayload(t, "/api/v1/pods?watch=true"),
	}, func(*protocol.Message) error { return nil })
	if err != nil {
		t.Fatalf("HandleStreamRequest: %v", err)
	}

	assertNoIdentityHeaders(t, <-seen)
}

// TestImpersonationCapabilityDeniedWithoutGrant covers the agent half of the
// capability handshake (§8 item 6). No shipped privilege profile grants the
// `impersonate` verb — Phase 0 deliberately does not add it — so the probe must
// answer NO and the heartbeat must advertise the feature as DENIED. The server
// refuses to move a cluster to `enforce` on that basis.
func TestImpersonationCapabilityDeniedWithoutGrant(t *testing.T) {
	// The fake clientset returns a zero-valued review: Allowed=false. That is
	// exactly what a real apiserver returns for an agent without the verb.
	if probeImpersonationAllowed(context.Background(), fake.NewClientset()) {
		t.Fatal("probe must not report allowed without an impersonate grant")
	}
	if probeImpersonationAllowed(context.Background(), nil) {
		t.Fatal("a probe with no client must fail closed")
	}

	hb := &protocol.HeartbeatPayload{}
	applyImpersonationCapability(hb, false)
	if !containsString(hb.DeniedFeatures, protocol.FeatureImpersonation) {
		t.Fatalf("denied features = %v, want %q", hb.DeniedFeatures, protocol.FeatureImpersonation)
	}
	if containsString(hb.EnabledFeatures, protocol.FeatureImpersonation) {
		t.Fatalf("enabled features must not advertise a capability the agent lacks: %v", hb.EnabledFeatures)
	}

	hb = &protocol.HeartbeatPayload{}
	applyImpersonationCapability(hb, true)
	if !containsString(hb.EnabledFeatures, protocol.FeatureImpersonation) {
		t.Fatalf("enabled features = %v, want %q", hb.EnabledFeatures, protocol.FeatureImpersonation)
	}
}

// TestImpersonationProbeCaches proves the probe is not a per-heartbeat
// apiserver call: heartbeats default to every 30s, the probe TTL is an hour.
func TestImpersonationProbeCaches(t *testing.T) {
	client := fake.NewClientset()
	calls := 0
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		calls++
		return false, nil, nil
	})
	var p impersonationProbe
	for range 5 {
		p.allowedNow(context.Background(), client)
	}
	if calls != 1 {
		t.Fatalf("SSAR calls = %d, want 1 (cached for %s)", calls, impersonationProbeTTL)
	}
}
