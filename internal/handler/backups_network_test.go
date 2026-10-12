package handler

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type s3FixtureTransport func(*http.Request) (*http.Response, error)

func (f s3FixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBackupS3OperatorOriginIsIndependentOfStoredURL(t *testing.T) {
	policy, err := httpclient.ParseDestinationPolicy(`{"restricted":true,"origins":[{"url":"http://10.42.1.7:9000","cidrs":["10.42.1.0/24"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewBackupHandler(nil)
	if err = handler.SetS3Network(policy, nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	// Inject a response-only unit fixture after constructing the real client.
	// DNS/connect/response bounds are exercised by the canonical transport tests;
	// this does not claim an S3 runtime or RustFS operation qualification.
	handler.httpClient = &http.Client{Transport: s3FixtureTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Port() != "9000" || request.URL.Query().Get("list-type") != "2" || request.Header.Get("Authorization") == "" {
			t.Fatal("probe lost origin or SigV4")
		}
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("UPSTREAM_FIXTURE_PRIVATE_DIAGNOSTIC")), Header: make(http.Header)}, nil
	})}
	config := sqlc.BackupStorageConfig{EndpointUrl: "http://10.42.1.7:9000", Bucket: "fixture", Region: "us-east-1"}
	err = handler.probeS3Bucket(t.Context(), config, "fixture-access", "fixture-secret")
	if err == nil || strings.Contains(err.Error(), "UPSTREAM_FIXTURE") || strings.Contains(err.Error(), "10.42.") || calls != 1 {
		t.Fatalf("probe redaction failed: calls=%d error=%v", calls, err)
	}
	for _, endpoint := range []string{"http://10.42.1.7:9001", "http://10.42.1.8:9000", "http://127.0.0.1:9000", "http://169.254.169.254:9000", "http://user:password@10.42.1.7:9000", "http://10.42.1.7:9000?token=fixture"} {
		config.EndpointUrl = endpoint
		if err = handler.probeS3Bucket(t.Context(), config, "fixture-access", "fixture-secret"); err == nil {
			t.Fatal("stored URL widened operator authority")
		}
	}
	if calls != 1 {
		t.Fatal("denied endpoint reached probe transport")
	}
}
