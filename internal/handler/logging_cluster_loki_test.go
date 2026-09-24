package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestClusterLokiQueryUsesOwnerAndScopesSelectors(t *testing.T) {
	id := uuid.New()
	requester := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		if req.ClusterID != id.String() {
			t.Fatalf("cluster=%s", req.ClusterID)
		}
		u, err := url.Parse(req.Path)
		if err != nil {
			t.Fatal(err)
		}
		if u.Path != "/api/v1/namespaces/logging/services/http:demo-loki:3100/proxy/loki/api/v1/query_range" {
			t.Fatalf("path=%s", u.Path)
		}
		q := u.Query().Get("query")
		if !strings.Contains(q, `cluster="`+id.String()+`"`) || !strings.Contains(q, "namespace=") {
			t.Fatalf("unscoped query %s", q)
		}
		return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString([]byte(`{"status":"success","data":{"result":[]}}`))}, nil
	}}
	h := &LoggingHandler{requester: requester}
	output := sqlc.LoggingOutput{ClusterID: pgtype.UUID{Bytes: id, Valid: true}, OutputType: "loki", Configuration: json.RawMessage(`{"host":"demo-loki.logging.svc.cluster.local","port":"3100","tls":"off","scheme":"http"}`)}
	if _, err := h.queryLoggingOutput(context.Background(), output, loggingQueryRequest{Query: `{job="demo"}`, Namespaces: []string{"demo"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.queryLoggingOutput(context.Background(), output, loggingQueryRequest{Query: `{cluster="another-cluster"}`}); err == nil {
		t.Fatal("cross-cluster query accepted")
	}
}
