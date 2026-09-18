package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
)

func TestInvalidOperatorInputNeverReachesServer(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"users", "create"}, "--email is required"},
		{[]string{"users", "create", "--email=alice@example.com"}, "--password is required"},
		{[]string{"users", "get", "not-a-uuid"}, "invalid"},
		{[]string{"users", "update", operatorTestID}, "nothing to update"},
		{[]string{"backup", "get", "not-a-uuid"}, "invalid"},
		{[]string{"backup", "create"}, "--name is required"},
		{[]string{"backup", "create", "--name=nightly", "--storage-id=invalid"}, "--storage-id"},
		{[]string{"rbac", "global-roles", "create"}, "--name is required"},
		{[]string{"rbac", "global-roles", "get", "not-a-uuid"}, "invalid role id"},
		{[]string{"rbac", "global-bindings", "create", "--role-id=" + operatorTestID}, "one of --user-id or --group"},
		{[]string{"rbac", "global-bindings", "create", "--role-id=" + operatorTestID, "--user-id=invalid"}, "invalid user id"},
		{[]string{"projects", "get", "not-a-uuid"}, "invalid project id"},
		{[]string{"projects", "create", "payments"}, "required flag"},
		{[]string{"nodes", "cordon", "not-a-uuid", "worker-1"}, "invalid cluster id"},
		{[]string{"workloads", "get", "not-a-uuid", "Deployment", "payments", "api"}, "invalid"},
		{[]string{"workloads", "scale", operatorTestID, "Deployment", "payments", "api"}, "--replicas is required"},
		{[]string{"settings", "general", "set"}, "nothing to update"},
		{[]string{"settings", "sso", "create", "company", "--type=invalid", "--client-id=id", "--client-secret=secret"}, "invalid --type"},
		{[]string{"admin", "smtp", "update"}, "--data is required"},
		{[]string{"admin", "smtp", "update", "--data=@/does-not-exist/astro.json"}, "no such file"},
		{[]string{"admin", "vault", "get", "not-a-uuid"}, "invalid id"},
		{[]string{"cluster-agent", "upgrade", operatorTestID, "--data={invalid"}, "JSON"},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid input sent %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			saveCLIConfig(t, &astrocli.Config{ServerURL: server.URL, AccessToken: "test-token"})
			t.Setenv("ASTRO_API_TOKEN", "")
			root := newRootCmd()
			root.SetArgs(test.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOperatorServerFailuresNeverProduceSuccessOutput(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		response, want string
	}{
		{"typed forbidden", 403, `{"error":{"code":"FORBIDDEN","message":"role is locked"}}`, "role is locked (FORBIDDEN)"},
		{"proxy unavailable", 502, "upstream disconnected", "502: upstream disconnected"},
		{"empty server error", 500, "", "status 500"},
		{"malformed success", 200, `{`, "unexpected end"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exchange := operatorExchange{args: []string{"rbac", "global-roles", "get", operatorTestID}, method: "GET", path: "/api/v1/rbac/global-roles/" + operatorTestID}
			output, err := runOperatorExchange(t, exchange, test.status, test.response)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if output != "" {
				t.Errorf("failure emitted %q", output)
			}
		})
	}
}

func TestSDKOutputGoldenFormats(t *testing.T) {
	payload := map[string]any{"name": "payments", "ready": true}
	for _, test := range []struct{ format, want string }{
		{"json", "{\n  \"name\": \"payments\",\n  \"ready\": true\n}\n"},
		{"yaml", "name: payments\nready: true\n"},
		{"table", "KEY    VALUE\nname   payments\nready  true\n"},
	} {
		t.Run(test.format, func(t *testing.T) {
			cmd := outputCommand(t, "--output="+test.format)
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := renderSDK(cmd, payload); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Errorf("output = %q; want %q", got, test.want)
			}
		})
	}
}
