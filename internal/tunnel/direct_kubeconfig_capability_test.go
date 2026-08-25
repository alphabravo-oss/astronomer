package tunnel

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestDirectKubeconfigCapabilityIsExactPathAndMethod(t *testing.T) {
	t.Parallel()
	capability := func(method, path string) string {
		body, err := json.Marshal(protocol.K8sRequestPayload{Method: method, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		return requiredCapabilityForMessage(&protocol.Message{Type: protocol.MsgK8sRequest, Payload: body})
	}
	wantPath := "/api/v1/namespaces/astronomer-system/serviceaccounts/astronomer-direct-reader/token"
	if got := capability(http.MethodPost, wantPath); got != protocol.FeatureDirectKubeconfig {
		t.Fatalf("capability = %q, want %q", got, protocol.FeatureDirectKubeconfig)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, wantPath},
		{http.MethodPost, "/api/v1/namespaces/astronomer-system/serviceaccounts/astronomer-agent/token"},
		{http.MethodPost, wantPath + "?dryRun=All"},
	} {
		if got := capability(tc.method, tc.path); got == protocol.FeatureDirectKubeconfig {
			t.Fatalf("%s %s incorrectly received direct capability", tc.method, tc.path)
		}
	}
}
