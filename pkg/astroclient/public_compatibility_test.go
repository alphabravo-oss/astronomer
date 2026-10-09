package astroclient_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// Existing v1 callers construct optional inhibition fields with pointers.
func TestLegacyInhibitionPointerMarshal(t *testing.T) {
	matchers := []client.InhibitionMatcher{{Label: "service", Value: ""}}
	labels := []string{"cluster"}
	enabled := true
	request := client.InhibitionRequest{Name: "existing", SourceMatchers: &matchers, TargetMatchers: &matchers, EqualLabels: &labels, Enabled: &enabled}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source_matchers", "target_matchers", "equal_labels", "enabled"} {
		if _, exists := wire[field]; !exists {
			t.Fatalf("existing field %s omitted", field)
		}
	}
	var roundTrip client.InhibitionRequest
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, roundTrip) {
		t.Fatal("existing pointer round trip changed")
	}
	empty := []client.InhibitionMatcher{}
	request.SourceMatchers = &empty
	encoded, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["source_matchers"]) != "[]" {
		t.Fatal("existing nonnil empty slice must marshal as array")
	}
	request.SourceMatchers = nil
	encoded, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	wire = nil
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, exists := wire["source_matchers"]; exists {
		t.Fatal("existing nil optional pointer must remain omitted")
	}
}
