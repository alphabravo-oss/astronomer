package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestScopeLogQLBindsEverySelector(t *testing.T) {
	clusterID := uuid.NewString()
	got, err := scopeLogQL(`sum(count_over_time({app="api"}[5m])) + count_over_time({job=~"worker.*"}[5m])`, clusterID, []string{"payments", "platform"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, `cluster="`+clusterID+`"`) != 2 {
		t.Fatalf("query did not bind every selector: %s", got)
	}
	if strings.Count(got, `namespace=~"payments|platform"`) != 2 {
		t.Fatalf("query did not bind namespaces: %s", got)
	}
}

func TestScopeLogQLRejectsCrossClusterMatcher(t *testing.T) {
	if _, err := scopeLogQL(`{cluster!="victim"}`, uuid.NewString(), nil); err == nil {
		t.Fatal("expected cross-cluster matcher to be rejected")
	}
}

func TestLoggingCapabilitiesExposeShippingOnlyProviders(t *testing.T) {
	if got := loggingCapabilitiesFor(loggingOutput("syslog", false, nil)); got.Query || got.QueryMode != "shipping_only" {
		t.Fatalf("syslog capabilities = %+v", got)
	}
	if got := loggingCapabilitiesFor(loggingOutput("loki", true, nil)); !got.Query || !got.Tail || !got.RetentionVisibility {
		t.Fatalf("system Loki capabilities = %+v", got)
	}
	if got := loggingCapabilitiesFor(loggingOutput("splunk", false, json.RawMessage(`{"hec_url":"https://splunk.example"}`))); got.Query || got.QueryMode != "link_out" {
		t.Fatalf("HEC-only Splunk capabilities = %+v", got)
	}
}

func TestLoggingCapabilitiesExposeSecureDatadogAndCloudWatchLinks(t *testing.T) {
	clusterID := uuid.New()
	datadog := loggingOutput("datadog", false, json.RawMessage(`{
		"site":"us3.datadoghq.com","service":"checkout","source":"kubernetes",
		"api_key":"must-not-leak","application_key":"must-not-leak-either"
	}`))
	datadog.ClusterID = pgtype.UUID{Bytes: clusterID, Valid: true}
	dd := loggingCapabilitiesFor(datadog)
	if !dd.LinkOut || dd.Query || dd.QueryMode != "link_out" {
		t.Fatalf("Datadog capabilities = %+v", dd)
	}
	parsed, err := url.Parse(dd.LinkOutURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "us3.datadoghq.com" || parsed.Path != "/logs" {
		t.Fatalf("Datadog link = %q", dd.LinkOutURL)
	}
	query := parsed.Query().Get("query")
	for _, want := range []string{"cluster:" + clusterID.String(), "service:checkout", "source:kubernetes"} {
		if !strings.Contains(query, want) {
			t.Fatalf("Datadog query %q missing %q", query, want)
		}
	}
	if strings.Contains(dd.LinkOutURL, "must-not-leak") {
		t.Fatalf("Datadog link leaked a credential: %s", dd.LinkOutURL)
	}

	cloudwatch := loggingOutput("cloudwatch", false, json.RawMessage(`{
		"region":"us-east-1","log_group":"/kubernetes/prod","secret_key":"must-not-leak"
	}`))
	cw := loggingCapabilitiesFor(cloudwatch)
	if !cw.LinkOut || cw.Query || cw.QueryMode != "link_out" {
		t.Fatalf("CloudWatch capabilities = %+v", cw)
	}
	parsed, err = url.Parse(cw.LinkOutURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "us-east-1.console.aws.amazon.com" ||
		parsed.Query().Get("region") != "us-east-1" || parsed.Fragment != "logsV2:logs-insights" {
		t.Fatalf("CloudWatch link = %q", cw.LinkOutURL)
	}
	if strings.Contains(cw.LinkOutURL, "must-not-leak") {
		t.Fatalf("CloudWatch link leaked a credential: %s", cw.LinkOutURL)
	}
}

func TestLoggingLinkOutRejectsUntrustedSitesAndPartitionMismatch(t *testing.T) {
	for _, tc := range []sqlc.LoggingOutput{
		loggingOutput("datadog", false, json.RawMessage(`{"site":"evil.example"}`)),
		loggingOutput("datadog", false, json.RawMessage(`{"site":"https://app.datadoghq.com@evil.example"}`)),
		loggingOutput("cloudwatch", false, json.RawMessage(`{"region":"us-east-1.evil.example"}`)),
		loggingOutput("cloudwatch", false, json.RawMessage(`{"region":"us-east-1","partition":"aws-us-gov"}`)),
	} {
		got := loggingCapabilitiesFor(tc)
		if got.LinkOut || got.LinkOutURL != "" || got.QueryMode != "shipping_only" {
			t.Fatalf("untrusted output capabilities = %+v", got)
		}
	}
}

func loggingOutput(outputType string, system bool, cfg json.RawMessage) sqlc.LoggingOutput {
	return sqlc.LoggingOutput{OutputType: outputType, IsSystem: system, Configuration: cfg}
}

type systemLokiQueryRequester struct {
	clusterID string
	path      string
	headers   map[string]string
}

func (r *systemLokiQueryRequester) Do(_ context.Context, clusterID, method, path string, _ []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	r.clusterID = clusterID
	r.path = path
	r.headers = headers
	if method != http.MethodGet {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusMethodNotAllowed}, nil
	}
	body := `{"status":"success","data":{"resultType":"streams","result":[]}}`
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
}

func TestQuerySystemLokiUsesManagementClusterAndTenantHeader(t *testing.T) {
	managedClusterID := uuid.NewString()
	managementClusterID := uuid.NewString()
	requester := &systemLokiQueryRequester{}
	h := NewLoggingHandler(nil)
	h.SetK8sRequester(requester)
	h.SetLokiAttachGate(&stubLokiAttachGate{state: lokiAttachState{
		Status: "healthy", ManagementClusterID: managementClusterID,
		Namespace: "monitoring", ReleaseName: "astronomer-loki",
	}})
	result, err := h.querySystemLoki(context.Background(), managedClusterID, loggingQueryRequest{Query: `{cluster="` + managedClusterID + `"}`})
	if err != nil {
		t.Fatal(err)
	}
	if result["backend"] != "astronomer_loki" {
		t.Fatalf("result = %+v", result)
	}
	if requester.clusterID != managementClusterID {
		t.Fatalf("routed cluster = %q, want management cluster %q", requester.clusterID, managementClusterID)
	}
	if requester.headers["X-Scope-OrgID"] != managedClusterID {
		t.Fatalf("tenant header = %q", requester.headers["X-Scope-OrgID"])
	}
	u, err := url.Parse("https://kubernetes.invalid" + requester.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u.Path, "/services/http:astronomer-loki-gateway:80/proxy/loki/api/v1/query_range") {
		t.Fatalf("query path = %s", requester.path)
	}
	if u.Query().Get("query") != `{cluster="`+managedClusterID+`"}` {
		t.Fatalf("proxied query = %q", u.Query().Get("query"))
	}
}
