package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestOperatorMutationsPreserveScopeAndExplicitValues(t *testing.T) {
	id := operatorTestID
	user := "/api/v1/users/" + id
	node := "/api/v1/nodes/" + id + "/worker-1"
	workload := "/api/v1/clusters/" + id + "/workloads/Deployment/payments/api"
	tests := []operatorExchange{
		{args: []string{"users", "create", "--email=alice@example.com", "--password=secret", "--username=alice", "--first-name=Alice", "--last-name=Smith", "--active=false", "--staff=true", "--superuser=false"}, method: "POST", path: "/api/v1/users", body: `{"email":"alice@example.com","password":"secret","username":"alice","first_name":"Alice","last_name":"Smith","is_active":false,"is_staff":true,"is_superuser":false}`, status: 201},
		{args: []string{"users", "update", id, "--active=false", "--first-name="}, method: "PATCH", path: user, body: `{"is_active":false,"first_name":""}`, status: 200},
		{args: []string{"users", "update", id, "--replace", "--email=alice@example.com", "--username=alice", "--first-name=Alice", "--last-name=Smith", "--active=true"}, method: "PUT", path: user, body: `{"email":"alice@example.com","username":"alice","first_name":"Alice","last_name":"Smith","is_active":true}`, status: 200},
		{args: []string{"users", "reset-password", id, "--password=changed"}, method: "POST", path: user + "/reset-password", body: `{"password":"changed"}`, status: 200},
		{args: []string{"rbac", "global-roles", "create", "--name=reader", "--display-name=Read only"}, method: "POST", path: "/api/v1/rbac/global-roles", body: `{"name":"reader","display_name":"Read only"}`, status: 201},
		{args: []string{"rbac", "global-roles", "update", id, "--name=reader"}, method: "PUT", path: "/api/v1/rbac/global-roles/" + id, body: `{"name":"reader"}`, status: 200},
		{args: []string{"rbac", "cluster-bindings", "create", "--role-id=" + id, "--cluster-id=" + id, "--group=operators"}, method: "POST", path: "/api/v1/rbac/cluster-bindings", body: `{"role_id":"` + id + `","cluster_id":"` + id + `","group":"operators"}`, status: 201},
		{args: []string{"rbac", "project-bindings", "create", "--role-id=" + id, "--project-id=" + id, "--user-id=" + id}, method: "POST", path: "/api/v1/rbac/project-bindings", body: `{"role_id":"` + id + `","project_id":"` + id + `","user_id":"` + id + `"}`, status: 201},
		{args: []string{"projects", "create", "payments", "--cluster=" + id, "--description=Payment services", "--pod-security-profile=restricted", "--namespace=payments,settlement"}, method: "POST", path: "/api/v1/projects/", body: `{"name":"payments","cluster_id":"` + id + `","description":"Payment services","pod_security_profile":"restricted","namespaces":["payments","settlement"]}`, status: 201},
		{args: []string{"projects", "update", id, "--quota-pods=0", "--quota-cpu=4", "--quota-memory=8Gi"}, method: "PUT", path: "/api/v1/projects/" + id + "/", body: `{"resource_quota_pod_count":0,"resource_quota_cpu_limit":"4","resource_quota_memory_limit":"8Gi"}`, status: 200},
		{args: []string{"backup", "create", "--name=nightly", "--storage-id=" + id, "--backup-type=full", "--included-namespaces=payments,settlement", "--excluded-namespaces=kube-system"}, method: "POST", path: "/api/v1/backups", body: `{"name":"nightly","storage_id":"` + id + `","backup_type":"full","included_namespaces":["payments","settlement"],"excluded_namespaces":["kube-system"]}`, status: 201},
		{args: []string{"nodes", "cordon", id, "worker-1"}, method: "POST", path: node + "/cordon/", status: 202, idempotent: true},
		{args: []string{"nodes", "uncordon", id, "worker-1"}, method: "POST", path: node + "/uncordon/", status: 202, idempotent: true},
		{args: []string{"nodes", "drain", id, "worker-1", "--dry-run", "--ignore-daemonsets", "--delete-emptydir-data=false", "--grace-period=0"}, method: "POST", path: node + "/drain/", body: `{"dry_run":true,"ignore_daemonsets":true,"delete_empty_dir_data":false,"grace_period_seconds":0}`, status: 200, idempotent: true},
		{args: []string{"nodes", "drain", id, "worker-1"}, method: "POST", path: node + "/drain/", body: `{"dry_run":false}`, status: 202, idempotent: true},
		{args: []string{"nodes", "label", id, "worker-1", "team", "payments"}, method: "POST", path: node + "/labels/", body: `{"key":"team","value":"payments"}`, status: 202, idempotent: true},
		{args: []string{"workloads", "scale", id, "Deployment", "payments", "api", "--replicas=0"}, method: "PATCH", path: workload + "/scale/", body: `{"replicas":0}`, status: 202, idempotent: true},
		{args: []string{"workloads", "restart", id, "Deployment", "payments", "api"}, method: "POST", path: workload + "/restart/", status: 202, idempotent: true},
		{args: []string{"workloads", "operations", "retry", id}, method: "POST", path: "/api/v1/workloads/operations/" + id + "/retry/", status: 202, idempotent: true},
		{args: []string{"settings", "general", "set", "--platform-name=Operations", "--agent-heartbeat-interval=15", "--default-session-timeout=0", "--enable-audit-logging=false", "--metrics-collection=true"}, method: "PUT", path: "/api/v1/settings/general", body: `{"agentHeartbeatInterval":15,"defaultSessionTimeout":0,"enableAuditLogging":false,"metricsCollection":true,"platformName":"Operations"}`, status: 200},
		{args: []string{"settings", "tokens", "create", "automation", "--expires-in-days=7", "--allowed-cidrs=10.0.0.0/8", "--scope=clusters:read,secrets:read"}, method: "POST", path: "/api/v1/settings/tokens", body: `{"name":"automation","expires_in_days":7,"allowed_cidrs":"10.0.0.0/8","scopes":["clusters:read","secrets:read"]}`, status: 201},
		{args: []string{"admin", "smtp", "update", "--data", `{"enabled":false,"host":"smtp.example.com","port":587}`}, method: "PUT", path: "/api/v1/admin/smtp", body: `{"enabled":false,"host":"smtp.example.com","port":587}`, status: 200},
		{args: []string{"admin", "smtp", "test", "--recipient=alice@example.com"}, method: "POST", path: "/api/v1/admin/smtp/test", body: `{"recipient":"alice@example.com"}`, status: 200},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args[:2], " ")+"/"+test.method+"/"+test.body, func(t *testing.T) {
			for _, status := range []int{test.status, http.StatusForbidden} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					response := `{"data":{}}`
					if status == http.StatusForbidden {
						response = `{"error":{"code":"FORBIDDEN","message":"permission denied"}}`
					}
					output, err := runOperatorExchange(t, test, status, response)
					if status == http.StatusForbidden {
						if err == nil || output != "" {
							t.Fatalf("denied mutation: output=%q, error=%v", output, err)
						}
					} else if err != nil {
						t.Fatal(err)
					} else if !json.Valid([]byte(output)) {
						t.Errorf("invalid JSON output: %q", output)
					}
				})
			}
		})
	}
}
