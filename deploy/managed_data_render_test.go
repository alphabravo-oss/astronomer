package deploy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCloudNativePGModeRendersSingleOrHAClusterAndGeneratedAppSecretConsumers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		instances string
	}{
		{name: "single", instances: "1"},
		{name: "ha", instances: "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t,
				"postgres.mode=cloudNativePG",
				"postgres.cloudNativePG.instances="+tc.instances,
			))
			cluster := findRenderedDoc(t, docs, "Cluster", "astronomer-postgres")
			if got := fmt.Sprint(nestedMap(cluster, "spec")["instances"]); got != tc.instances {
				t.Fatalf("CNPG instances = %q, want %s", got, tc.instances)
			}
			if renderedDocExists(docs, "StatefulSet", "astronomer-postgres") {
				t.Fatal("CloudNativePG mode also rendered legacy bundled PostgreSQL")
			}
			postgresPolicy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-postgres")
			policyJSON, _ := json.Marshal(postgresPolicy)
			if !strings.Contains(string(policyJSON), `"kubernetes.io/metadata.name":"cnpg-system"`) || !strings.Contains(string(policyJSON), `"port":8000`) {
				t.Fatal("CloudNativePG policy does not admit the operator instance-manager status connection")
			}
			for _, workload := range []struct{ kind, name string }{
				{kind: "Deployment", name: "astronomer-server"},
				{kind: "Deployment", name: "astronomer-worker"},
				{kind: "Job", name: "astronomer-migrate"},
			} {
				doc := findRenderedDoc(t, docs, workload.kind, workload.name)
				raw, _ := json.Marshal(doc)
				if !strings.Contains(string(raw), `"name":"astronomer-postgres-app"`) || !strings.Contains(string(raw), `"key":"uri"`) {
					t.Fatalf("%s/%s does not consume the CNPG app URI Secret", workload.kind, workload.name)
				}
			}
		})
	}
}

func TestCloudNativePGBarmanCloudBackupAndPITRAreOptional(t *testing.T) {
	defaults := parseRenderedDocs(t, helmTemplate(t, "postgres.mode=cloudNativePG"))
	cluster := findRenderedDoc(t, defaults, "Cluster", "astronomer-postgres")
	if _, ok := nestedMap(cluster, "spec")["plugins"]; ok {
		t.Fatal("Barman Cloud plugin rendered by default")
	}
	if renderedDocExists(defaults, "ScheduledBackup", "astronomer-postgres") {
		t.Fatal("ScheduledBackup rendered by default")
	}

	docs := parseRenderedDocs(t, helmTemplate(t,
		"postgres.mode=cloudNativePG",
		"postgres.cloudNativePG.barmanCloud.enabled=true",
		"postgres.cloudNativePG.barmanCloud.objectStoreName=astronomer-backups",
		"postgres.cloudNativePG.barmanCloud.scheduledBackup.enabled=true",
		"postgres.cloudNativePG.barmanCloud.recovery.enabled=true",
		"postgres.cloudNativePG.barmanCloud.recovery.objectStoreName=astronomer-restore",
		"postgres.cloudNativePG.barmanCloud.recovery.serverName=source-cluster",
		"postgres.cloudNativePG.barmanCloud.recovery.targetTime=2026-08-25T12:00:00Z",
	))
	rendered, _ := json.Marshal(docs)
	text := string(rendered)
	for _, required := range []string{
		`"name":"barman-cloud.cloudnative-pg.io"`,
		`"barmanObjectName":"astronomer-backups"`,
		`"barmanObjectName":"astronomer-restore"`,
		`"serverName":"source-cluster"`,
		`"targetTime":"2026-08-25T12:00:00Z"`,
		`"method":"plugin"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Barman Cloud render missing %s", required)
		}
	}
	findRenderedDoc(t, docs, "ScheduledBackup", "astronomer-postgres")
}

func TestValkeySentinelModeRendersAuthenticatedThreeNodeFailover(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"redis.mode=sentinel",
	))
	findRenderedDoc(t, docs, "Secret", "astronomer-valkey-auth")
	data := findRenderedDoc(t, docs, "StatefulSet", "astronomer-valkey")
	if got := fmt.Sprint(nestedMap(data, "spec")["replicas"]); got != "3" {
		t.Fatalf("Valkey replicas = %q, want 3", got)
	}
	sentinel := findRenderedDoc(t, docs, "Deployment", "astronomer-redis-sentinel")
	if got := fmt.Sprint(nestedMap(sentinel, "spec")["replicas"]); got != "3" {
		t.Fatalf("Sentinel replicas = %q, want 3", got)
	}
	for _, name := range []string{"astronomer-valkey", "astronomer-redis-sentinel"} {
		pdb := findRenderedDoc(t, docs, "PodDisruptionBudget", name)
		if got := fmt.Sprint(nestedMap(pdb, "spec")["minAvailable"]); got != "2" {
			t.Fatalf("PDB/%s minAvailable = %q, want 2", name, got)
		}
	}
	rendered, _ := json.Marshal(docs)
	text := string(rendered)
	for _, required := range []string{
		"--appendonly", "--min-replicas-to-write", "--replicaof",
		"sentinel monitor astronomer", "sentinel auth-pass astronomer",
		"redis-sentinel://astronomer-redis-sentinel:26379?master=astronomer",
		"password_env=REDIS_PASSWORD",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Sentinel render missing %q", required)
		}
	}
	if strings.Contains(text, "shared-secret") || strings.Contains(text, "data-secret") {
		t.Fatal("Sentinel render contains test credential material")
	}
	if renderedDocExists(docs, "Service", "astronomer-redis") {
		t.Fatal("Sentinel mode rendered legacy single-node Redis Service")
	}
}

func TestManagedDataModesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		sets []string
		want string
	}{
		{sets: []string{"postgres.mode=invalid"}, want: "value must be one of"},
		{sets: []string{"redis.mode=sentinel", "redis.sentinel.passwordSecretRef.name=x", "redis.sentinel.replicas=2"}, want: "minimum: got 2, want 3"},
	} {
		errOut := helmTemplateExpectError(t, nil, tc.sets...)
		if !strings.Contains(errOut, tc.want) {
			t.Fatalf("render error missing %q:\n%s", tc.want, errOut)
		}
	}
}

func TestBootstrapForcePasswordChangeRenders(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t, "bootstrap.forcePasswordChange=true"))
	server := findRenderedDoc(t, docs, "Deployment", "astronomer-server")
	rendered, _ := json.Marshal(server)
	text := string(rendered)
	if !strings.Contains(text, "ASTRONOMER_BOOTSTRAP_FORCE_PASSWORD_CHANGE") || !strings.Contains(text, `"value":"true"`) {
		t.Fatalf("server render missing enabled bootstrap password-change policy: %s", text)
	}
}
