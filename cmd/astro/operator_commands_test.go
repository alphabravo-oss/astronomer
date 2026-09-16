package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

const operatorTestID = "11111111-1111-4111-8111-111111111111"

type operatorExchange struct {
	args                      []string
	method, path, query, body string
	status                    int
	response                  string
	idempotent                bool
}

// Exercise the shipped command tree, configuration resolution, generated SDK,
// and output together. No test redirects process stdin/stdout or shares config.
func runOperatorExchange(t *testing.T, exchange operatorExchange, status int, response string) (string, error) {
	t.Helper()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != exchange.method || r.URL.Path != exchange.path {
			t.Errorf("request = %s %s; want %s %s", r.Method, r.URL.Path, exchange.method, exchange.path)
		}
		wantQuery, err := url.ParseQuery(exchange.query)
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(r.URL.Query(), wantQuery) {
			t.Errorf("query = %v; want %v", r.URL.Query(), wantQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer isolated-token" {
			t.Errorf("Authorization = %q", got)
		}
		if exchange.idempotent && r.Header.Get("Idempotency-Key") == "" {
			t.Error("mutation missing Idempotency-Key")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if exchange.body != "" {
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			assertOperatorJSON(t, body, []byte(exchange.body))
		} else if len(body) != 0 {
			t.Errorf("unexpected body: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	saveCLIConfig(t, &astrocli.Config{ServerURL: server.URL, AccessToken: "isolated-token"})
	t.Setenv("ASTRO_API_TOKEN", "")
	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(append([]string{}, exchange.args...), "--output=json"))
	err := root.Execute()
	if requests != 1 {
		t.Errorf("HTTP requests = %d, want exactly one", requests)
	}
	return output.String(), err
}

func assertOperatorJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("invalid JSON %q: %v", got, err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s; want %s", got, want)
	}
}

func TestOperatorCanonicalHistoryPages(t *testing.T) {
	for _, exchange := range []operatorExchange{
		{args: []string{"admin", "emails", "list", "--limit=1", "--offset=2"}, method: http.MethodGet, path: "/api/v1/admin/emails", query: "limit=1&offset=2"},
		{args: []string{"admin", "webhook", "deliveries", operatorTestID, "--limit=1", "--offset=2"}, method: http.MethodGet, path: "/api/v1/admin/webhooks/" + operatorTestID + "/deliveries", query: "limit=1&offset=2"},
	} {
		t.Run(strings.Join(exchange.args, " "), func(t *testing.T) {
			body := `{"data":[{"id":"` + operatorTestID + `"}],"pagination":{"total":3,"limit":1,"offset":2,"has_more":false,"next_offset":null}}`
			output, err := runOperatorExchange(t, exchange, http.StatusOK, body)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(output), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0]["id"] != operatorTestID {
				t.Fatalf("list output = %s", output)
			}
		})
	}
}

func TestOperatorReadCommandsHTTPContract(t *testing.T) {
	id := operatorTestID
	cluster := "/api/v1/clusters/" + id
	tests := []operatorExchange{
		{args: []string{"rbac", "global-roles", "list", "--limit=7", "--offset=2"}, path: "/api/v1/rbac/global-roles", query: "limit=7&offset=2", response: `{"data":[{"id":"` + id + `","name":"reader"}]}`},
		{args: []string{"rbac", "global-roles", "get", id}, path: "/api/v1/rbac/global-roles/" + id, response: `{"data":{"id":"` + id + `","name":"reader"}}`},
		{args: []string{"rbac", "templates", "list"}, path: "/api/v1/rbac/templates", response: `{"data":{"templates":[],"count":0}}`},
		{args: []string{"users", "list", "--limit=7", "--offset=2"}, path: "/api/v1/users", query: "limit=7&offset=2", response: `{"data":[{"id":"` + id + `","username":"alice","email":"alice@example.com"}]}`},
		{args: []string{"users", "get", id}, path: "/api/v1/users/" + id, response: `{"data":{"id":"` + id + `","username":"alice","email":"alice@example.com"}}`},
		{args: []string{"projects", "list", "--limit=7", "--offset=2"}, path: "/api/v1/projects/", query: "limit=7&offset=2", response: `{"data":[]}`},
		{args: []string{"projects", "get", id}, path: "/api/v1/projects/" + id + "/", response: `{"data":{"id":"` + id + `","name":"payments"}}`},
		{args: []string{"backup", "list", "--limit=7", "--offset=0"}, path: "/api/v1/backups", query: "limit=7&offset=0", response: `{"data":[]}`},
		{args: []string{"backup", "get", id}, path: "/api/v1/backups/" + id, response: `{"data":{"id":"` + id + `","name":"nightly"}}`},
		{args: []string{"backup", "schedules", "list"}, path: "/api/v1/backups/schedules", response: `{"data":[]}`},
		{args: []string{"backup", "storage", "list"}, path: "/api/v1/backups/storage", response: `{"data":[]}`},
		{args: []string{"nodes", "list", id}, path: cluster + "/nodes/", response: `{"data":[]}`},
		{args: []string{"nodes", "get", id, "worker-1"}, path: cluster + "/nodes/worker-1/", response: `{"data":{"name":"worker-1"}}`},
		{args: []string{"workloads", "list", id, "--namespace=payments", "--kind=Deployment", "--search=api"}, path: cluster + "/workloads/", query: "namespace=payments&kind=Deployment&search=api", response: `{"data":[]}`},
		{args: []string{"workloads", "get", id, "Deployment", "payments", "api"}, path: cluster + "/workloads/Deployment/payments/api/", response: `{"data":{"name":"api"}}`},
		{args: []string{"workloads", "pods", id, "--namespace=payments"}, path: cluster + "/pods/", query: "namespace=payments", response: `{"data":[]}`},
		{args: []string{"workloads", "operations", "list", "--status=failed", "--target-type=Deployment", "--target-key=api", "--limit=7", "--offset=2"}, path: "/api/v1/workloads/operations/", query: "status=failed&targetType=Deployment&targetKey=api&limit=7&offset=2", response: `{"data":[]}`},
		{args: []string{"workloads", "operations", "get", id}, path: "/api/v1/workloads/operations/" + id + "/", response: `{"data":{"id":"` + id + `","status":"failed"}}`},
		{args: []string{"monitoring", "health", id}, path: cluster + "/health", response: `{"data":{}}`},
		{args: []string{"monitoring", "metrics-summary", id}, path: cluster + "/metrics/summary", response: `{"data":{}}`},
		{args: []string{"monitoring", "conditions", id}, path: cluster + "/conditions", response: `{"data":[]}`},
		{args: []string{"monitoring", "events", id, "--limit=7"}, path: cluster + "/events/", query: "limit=7", response: `{"data":[]}`},
		{args: []string{"settings", "tokens", "list", "--limit=7", "--offset=2"}, path: "/api/v1/settings/tokens", query: "limit=7&offset=2", response: `{"data":[]}`},
		{args: []string{"settings", "sso", "list"}, path: "/api/v1/settings/sso", response: `[]`},
		{args: []string{"admin", "smtp", "get"}, path: "/api/v1/admin/smtp", response: `{"data":{"host":"smtp.example.com"}}`},
		{args: []string{"admin", "webhooks", "list"}, path: "/api/v1/admin/webhooks", response: `{"data":{"items":[],"total":0}}`},
		{args: []string{"admin", "vault", "list"}, path: "/api/v1/admin/vault-connections", response: `{"data":{"items":[]}}`},
		{args: []string{"admin", "key-status"}, path: "/api/v1/admin/key-status", response: `{"data":{}}`},
		{args: []string{"cluster-agent", "list", "--limit=7", "--offset=2"}, path: "/api/v1/cluster-agents/", query: "limit=7&offset=2", response: `{"data":[]}`},
		{args: []string{"cluster-agent", "diagnostics", id}, path: "/api/v1/cluster-agents/" + id + "/diagnostics/", response: `{"data":{"connected":true}}`},
	}
	for _, test := range tests {
		test.method = http.MethodGet
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			t.Run("success", func(t *testing.T) {
				output, err := runOperatorExchange(t, test, http.StatusOK, test.response)
				if err != nil {
					t.Fatal(err)
				}
				if !json.Valid([]byte(output)) {
					t.Errorf("not machine-readable JSON: %q", output)
				}
			})
			t.Run("denied", func(t *testing.T) {
				output, err := runOperatorExchange(t, test, http.StatusForbidden, `{"error":{"code":"FORBIDDEN","message":"permission denied"}}`)
				if err == nil {
					t.Fatal("denied operation reported success")
				}
				if output != "" {
					t.Errorf("denied operation emitted success output: %q", output)
				}
			})
		})
	}
}
