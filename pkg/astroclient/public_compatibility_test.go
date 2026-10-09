package astroclient_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// These are existing v1 consumer signatures, including the variadic editor API.
var _ interface {
	GetAuthMe(context.Context, ...client.RequestEditorFn) (*http.Response, error)
} = (*client.Client)(nil)

func TestExistingPublicClientAndNullableModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("existing request editor was not applied")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"username":"existing-client","last_login":null}}`)
	}))
	defer server.Close()
	c, err := client.NewClient(server.URL, client.WithHTTPClient(server.Client()), client.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer fixture")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := c.GetAuthMe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := client.ParseGetAuthMeResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.JSON200 == nil || parsed.JSON200.Data == nil || parsed.JSON200.Data.Username == nil || *parsed.JSON200.Data.Username != "existing-client" || parsed.JSON200.Data.LastLogin != nil {
		t.Fatalf("existing nullable user model changed: %#v", parsed.JSON200)
	}
	active, err := client.ParseAdminComplianceBaselineActiveResponse(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":{"active":null}}`))})
	if err != nil {
		t.Fatal(err)
	}
	if active.JSON200 == nil || active.JSON200.Data.Active != nil {
		t.Fatal("existing nullable active-baseline pointer contract changed")
	}
}
