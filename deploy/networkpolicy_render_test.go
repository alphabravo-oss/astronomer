package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestNetworkPolicyRendersNamespaceDefaultDeny(t *testing.T) {
	out := helmTemplate(t)

	for _, want := range []string{
		"name: astronomer-default-deny",
		"app.kubernetes.io/component: network-policy",
		"app.kubernetes.io/name: astronomer",
		"app.kubernetes.io/instance: astronomer",
		"app.kubernetes.io/part-of: astronomer",
		"- Ingress",
		"- Egress",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}

func TestNetworkPolicySupportsGranularExternalEgressCIDRs(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "networkpolicy-values.yaml")
	values := []byte(`
networkPolicy:
  externalEgressCIDRs: []
  externalHTTPSEgressCIDRs:
    - 10.50.0.0/16
  externalPostgresEgressCIDRs:
    - 10.20.0.0/16
  externalRedisEgressCIDRs:
    - 10.30.0.0/16
  kubernetesAPIEgressCIDRs:
    - 10.40.0.0/16
  identityProviderEgressCIDRs:
    - 10.60.0.0/16
dex:
  enabled: true
`)
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}

	out := helmTemplateWithValueFiles(t, []string{valuesPath})
	if strings.Contains(out, `cidr: "0.0.0.0/0"`) {
		t.Fatalf("granular override still rendered broad legacy egress:\n%s", out)
	}

	for _, want := range []string{
		`cidr: "10.50.0.0/16"`,
		`cidr: "10.20.0.0/16"`,
		`cidr: "10.30.0.0/16"`,
		`cidr: "10.40.0.0/16"`,
		`cidr: "10.60.0.0/16"`,
		"port: 443",
		"port: 5432",
		"port: 6379",
		"port: 6443",
		"port: 389",
		"port: 636",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered chart missing %q:\n%s", want, out)
		}
	}
}

func TestNetworkPolicyRendersExpectedComponentPolicies(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t, "dex.enabled=true"))

	defaultDeny := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-default-deny")
	selector := nestedMap(defaultDeny, "spec", "podSelector")
	if _, ok := selector["matchExpressions"]; ok {
		t.Fatalf("default-deny must not use negative or open-ended expressions: %#v", selector)
	}
	wantOwnership := map[string]any{
		"app.kubernetes.io/name":     "astronomer",
		"app.kubernetes.io/instance": "astronomer",
		"app.kubernetes.io/part-of":  "astronomer",
	}
	if got := nestedMap(defaultDeny, "spec", "podSelector", "matchLabels"); !reflect.DeepEqual(got, wantOwnership) {
		t.Fatalf("default-deny ownership selector = %#v, want exactly %#v", got, wantOwnership)
	}
	assertPolicyTypes(t, defaultDeny, "Ingress", "Egress")

	for _, tt := range []struct {
		name      string
		component string
	}{
		{name: "astronomer-frontend", component: "frontend"},
		{name: "astronomer-server", component: "server"},
		{name: "astronomer-worker", component: "worker"},
		{name: "astronomer-dex", component: "dex"},
		{name: "astronomer-postgres", component: "postgres"},
		{name: "astronomer-redis", component: "redis"},
	} {
		policy := findRenderedDoc(t, docs, "NetworkPolicy", tt.name)
		assertPolicyTypes(t, policy, "Ingress", "Egress")
		labels := nestedMap(policy, "spec", "podSelector", "matchLabels")
		if got := stringValue(labels["app.kubernetes.io/component"]); got != tt.component {
			t.Fatalf("%s selector component = %q, want %q", tt.name, got, tt.component)
		}
	}

	for _, name := range []string{"astronomer-postgres", "astronomer-redis"} {
		policy := findRenderedDoc(t, docs, "NetworkPolicy", name)
		rawEgress, _ := nestedMap(policy, "spec")["egress"].([]any)
		if len(rawEgress) != 0 {
			t.Fatalf("%s should not allow outbound egress, got %#v", name, rawEgress)
		}
	}
}

func TestDefaultDenySelectsOnlyAstronomerOwnedPlatformPods(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t, "dex.enabled=true"))
	defaultDeny := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-default-deny")
	selector := nestedMap(defaultDeny, "spec", "podSelector", "matchLabels")

	for _, workload := range []struct {
		kind string
		name string
	}{
		{kind: "Deployment", name: "astronomer-server"},
		{kind: "Deployment", name: "astronomer-worker"},
		{kind: "Deployment", name: "astronomer-frontend"},
		{kind: "Deployment", name: "astronomer-dex"},
		{kind: "StatefulSet", name: "astronomer-postgres"},
		{kind: "StatefulSet", name: "astronomer-redis"},
		{kind: "Job", name: "astronomer-migrate"},
		{kind: "Job", name: "astronomer-preflight"},
	} {
		doc := findRenderedDoc(t, docs, workload.kind, workload.name)
		labels := nestedStringMap(doc, "spec", "template", "metadata", "labels")
		if !matchExactLabels(selector, labels) {
			t.Errorf("default-deny does not select platform %s/%s labels %#v", workload.kind, workload.name, labels)
		}
	}
	argoServer := findRenderedDoc(t, docs, "Deployment", "astro-argocd-server")
	argoLabels := nestedStringMap(argoServer, "spec", "template", "metadata", "labels")
	if matchExactLabels(selector, argoLabels) {
		t.Errorf("default-deny unexpectedly captures rendered bundled Argo CD labels %#v", argoLabels)
	}

	for name, labels := range map[string]map[string]string{
		"bundled Argo CD": {
			"app.kubernetes.io/name":     "argocd-server",
			"app.kubernetes.io/instance": "astronomer",
			"app.kubernetes.io/part-of":  "argocd",
		},
		"NGF generated Gateway data plane": {
			"app.kubernetes.io/name":                 "astronomer-nginx",
			"app.kubernetes.io/instance":             "ngf",
			"app.kubernetes.io/managed-by":           "ngf-nginx",
			"gateway.networking.k8s.io/gateway-name": "astronomer",
		},
		"unlabeled namespace pod": {},
		"look-alike missing ownership": {
			"app.kubernetes.io/name":     "astronomer",
			"app.kubernetes.io/instance": "astronomer",
		},
		"different Astronomer release": {
			"app.kubernetes.io/name":     "astronomer",
			"app.kubernetes.io/instance": "other-release",
			"app.kubernetes.io/part-of":  "astronomer",
		},
	} {
		if matchExactLabels(selector, labels) {
			t.Errorf("default-deny unexpectedly captures %s labels %#v", name, labels)
		}
	}
}

func TestDefaultDenyOwnershipContractCoversEveryRenderedWorkloadPhase(t *testing.T) {
	tests := []struct {
		name          string
		sets          []string
		requiredKinds []string
	}{
		{
			name: "fresh",
			sets: []string{"dex.enabled=true", "dex.migration.phase=fresh"},
		},
		{
			name:          "prepare",
			sets:          []string{"dex.enabled=true", "dex.migration.phase=prepare"},
			requiredKinds: []string{"Job/astronomer-dex-legacy-prepare"},
		},
		{
			name:          "cutover",
			sets:          []string{"dex.enabled=true", "dex.migration.phase=cutover"},
			requiredKinds: []string{"Job/astronomer-dex-legacy-cleanup"},
		},
		{
			name: "optional workloads",
			sets: []string{
				"dex.enabled=true",
				"dex.migration.phase=fresh",
				"managementBackup.s3.bucket=enterprise-backups",
				"managementBackup.s3.credentialsSecretRef.name=enterprise-backup-credentials",
				"managementLogging.enabled=true",
				"managementLogging.endpoint=https://logs.example.invalid",
			},
			requiredKinds: []string{
				"CronJob/astronomer-management-backup",
				"CronJob/astronomer-restore-drill",
				"DaemonSet/astronomer-mgmt-logging",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t, tt.sets...))
			defaultDeny := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-default-deny")
			selector := nestedMap(defaultDeny, "spec", "podSelector", "matchLabels")
			seen := map[string]bool{}
			workloadCount := 0

			for _, doc := range docs {
				labels, ok := renderedWorkloadPodLabels(doc)
				if !ok {
					continue
				}
				workloadCount++
				identity := stringValue(doc["kind"]) + "/" + stringAt(doc, "metadata", "name")
				seen[identity] = true

				if labels["app.kubernetes.io/part-of"] == "argocd" {
					if matchExactLabels(selector, labels) {
						t.Errorf("default-deny captures bundled Argo workload %s labels %#v", identity, labels)
					}
					continue
				}

				if !matchExactLabels(selector, labels) {
					t.Errorf("rendered non-Argo workload %s is outside the exact Astronomer ownership contract: %#v", identity, labels)
				}
				if labels["app.kubernetes.io/component"] == "" {
					t.Errorf("rendered Astronomer workload %s has no dedicated component label: %#v", identity, labels)
				}
			}

			if workloadCount == 0 {
				t.Fatal("render contained no workload pod templates")
			}
			for _, required := range tt.requiredKinds {
				if !seen[required] {
					t.Errorf("scenario did not exercise required workload %s", required)
				}
			}
		})
	}
}

func renderedWorkloadPodLabels(doc renderedDoc) (map[string]string, bool) {
	switch stringValue(doc["kind"]) {
	case "Deployment", "StatefulSet", "DaemonSet", "Job":
		return nestedStringMap(doc, "spec", "template", "metadata", "labels"), true
	case "CronJob":
		return nestedStringMap(doc, "spec", "jobTemplate", "spec", "template", "metadata", "labels"), true
	default:
		return nil, false
	}
}

func TestDexMigrationHookNetworkPoliciesArePhaseScopedAndLeastPrivilege(t *testing.T) {
	const (
		prepareName = "astronomer-dex-legacy-prepare"
		cleanupName = "astronomer-dex-legacy-cleanup"
	)

	for _, tt := range []struct {
		phase        string
		presentName  string
		absentName   string
		component    string
		policyWeight int
		jobWeight    int
		helmHook     string
		argoHook     string
		argoDelete   string
		helmDelete   string
	}{
		{phase: "fresh", absentName: prepareName + "," + cleanupName},
		{
			phase: "prepare", presentName: prepareName, absentName: cleanupName,
			component: "dex-legacy-prepare", policyWeight: -10, jobWeight: -5,
			helmHook: "pre-upgrade", argoHook: "PreSync",
			argoDelete: "BeforeHookCreation", helmDelete: "before-hook-creation",
		},
		{
			phase: "cutover", presentName: cleanupName, absentName: prepareName,
			component: "dex-legacy-cleanup", policyWeight: 5, jobWeight: 10,
			helmHook: "post-upgrade", argoHook: "PostSync",
			argoDelete: "BeforeHookCreation", helmDelete: "before-hook-creation",
		},
	} {
		t.Run(tt.phase, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t,
				"dex.enabled=true",
				"dex.migration.phase="+tt.phase,
				"networkPolicy.externalEgressCIDRs[0]=10.10.0.0/16",
				"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/16",
			))

			for _, absent := range strings.Split(tt.absentName, ",") {
				if absent == "" {
					continue
				}
				if renderedDocExists(docs, "Job", absent) || renderedDocExists(docs, "NetworkPolicy", absent) {
					t.Errorf("%s rendered out-of-phase hook resources for %s", tt.phase, absent)
				}
			}
			if tt.presentName == "" {
				return
			}

			job := findRenderedDoc(t, docs, "Job", tt.presentName)
			policy := findRenderedDoc(t, docs, "NetworkPolicy", tt.presentName)
			jobLabels := nestedStringMap(job, "spec", "template", "metadata", "labels")
			for key, want := range map[string]string{
				"app.kubernetes.io/name":      "astronomer",
				"app.kubernetes.io/instance":  "astronomer",
				"app.kubernetes.io/part-of":   "astronomer",
				"app.kubernetes.io/component": tt.component,
			} {
				if got := jobLabels[key]; got != want {
					t.Errorf("%s pod label %s = %q, want %q", tt.presentName, key, got, want)
				}
			}
			selector := nestedMap(policy, "spec", "podSelector", "matchLabels")
			wantSelector := map[string]any{
				"app.kubernetes.io/name":      "astronomer",
				"app.kubernetes.io/instance":  "astronomer",
				"app.kubernetes.io/component": tt.component,
			}
			if !reflect.DeepEqual(selector, wantSelector) {
				t.Errorf("%s policy selector = %#v, want %#v", tt.presentName, selector, wantSelector)
			}
			if got, want := preflightEgressContracts(t, policy), []string{
				"*:UDP/53,TCP/53",
				"10.40.0.0/16:TCP/443",
				"10.10.0.0/16:TCP/443",
			}; !reflect.DeepEqual(got, want) {
				t.Errorf("%s egress = %v, want exactly %v", tt.presentName, got, want)
			}

			policyAnnotations := nestedMap(policy, "metadata", "annotations")
			jobAnnotations := nestedMap(job, "metadata", "annotations")
			for key, want := range map[string]string{
				"helm.sh/hook":                          tt.helmHook,
				"argocd.argoproj.io/hook":               tt.argoHook,
				"helm.sh/hook-delete-policy":            tt.helmDelete,
				"argocd.argoproj.io/hook-delete-policy": tt.argoDelete,
			} {
				if got := stringValue(policyAnnotations[key]); got != want {
					t.Errorf("%s policy annotation %s = %q, want %q", tt.presentName, key, got, want)
				}
			}
			policyWeight, policyErr := strconv.Atoi(stringValue(policyAnnotations["helm.sh/hook-weight"]))
			jobWeight, jobErr := strconv.Atoi(stringValue(jobAnnotations["helm.sh/hook-weight"]))
			if policyErr != nil || jobErr != nil || policyWeight != tt.policyWeight || jobWeight != tt.jobWeight || policyWeight >= jobWeight {
				t.Errorf("%s hook order policy=%d (%v), job=%d (%v), want %d before %d", tt.presentName, policyWeight, policyErr, jobWeight, jobErr, tt.policyWeight, tt.jobWeight)
			}
		})
	}
}

func TestDexMigrationHookPolicyFollowsDefaultDenySwitch(t *testing.T) {
	for _, phase := range []string{"prepare", "cutover"} {
		docs := parseRenderedDocs(t, helmTemplate(t,
			"dex.enabled=true",
			"dex.migration.phase="+phase,
			"networkPolicy.defaultDeny=false",
		))
		name := "astronomer-dex-legacy-" + phase
		if phase == "cutover" {
			name = "astronomer-dex-legacy-cleanup"
		}
		if !renderedDocExists(docs, "Job", name) {
			t.Errorf("%s Job disappeared when default deny was disabled", name)
		}
		if renderedDocExists(docs, "NetworkPolicy", name) {
			t.Errorf("%s NetworkPolicy rendered without the selecting default deny", name)
		}
	}
}

func TestDexMigrationHookPolicyDoesNotInventAPIFallbackAndDeduplicatesCIDRs(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "dex-hook-networkpolicy-values.yaml")
	values := []byte(`
networkPolicy:
  externalEgressCIDRs: []
  kubernetesAPIEgressCIDRs: []
dex:
  enabled: true
  migration:
    phase: prepare
`)
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}
	docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, []string{valuesPath}))
	policy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-dex-legacy-prepare")
	if got, want := preflightEgressContracts(t, policy), []string{"*:UDP/53,TCP/53"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Dex prepare policy invented API fallback: got %v, want %v", got, want)
	}

	docs = parseRenderedDocs(t, helmTemplate(t,
		"dex.enabled=true",
		"dex.migration.phase=cutover",
		"networkPolicy.externalEgressCIDRs[0]=10.40.0.0/16",
		"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/16",
	))
	policy = findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-dex-legacy-cleanup")
	if got, want := preflightEgressContracts(t, policy), []string{
		"*:UDP/53,TCP/53",
		"10.40.0.0/16:TCP/443",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Dex cleanup policy CIDR union = %v, want de-duplicated %v", got, want)
	}
}

func nestedStringMap(root map[string]any, path ...string) map[string]string {
	raw := nestedMap(root, path...)
	result := make(map[string]string, len(raw))
	for key, value := range raw {
		result[key] = stringValue(value)
	}
	return result
}

func matchExactLabels(selector map[string]any, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != stringValue(value) {
			return false
		}
	}
	return true
}

func TestProductionNetworkPolicyUsesGranularExternalDependencyCIDRs(t *testing.T) {
	prodValues := filepath.Join("chart", "values-production.yaml")
	out := helmTemplateWithValueFiles(t, []string{prodValues},
		"config.serverURL=https://astronomer.example.com",
		"gateway.hosts[0]=astronomer.example.com",
		"tls.source=secret",
		"tls.secretName=astronomer-tls",
		"postgres.external.dsnSecretRef.name=astronomer-postgres-dsn",
		"redis.external.address=redis.astronomer.svc.cluster.local:6379",
		"secrets.secretKey=prod-jwt-signing-key",
		"secrets.encryptionKey=prod-fernet-key",
		"bootstrap.email=admin@example.com",
		"bootstrap.password=prod-admin-initial",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		"managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap",
		"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
		"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/14",
	)
	if strings.Contains(out, `cidr: "0.0.0.0/0"`) {
		t.Fatalf("production render should not include broad external egress:\n%s", out)
	}
	for _, want := range []string{`cidr: "10.20.0.0/16"`, `cidr: "10.30.0.0/16"`, `cidr: "10.40.0.0/14"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("production render missing %q:\n%s", want, out)
		}
	}

	docs := parseRenderedDocs(t, out)
	for _, absent := range []string{"astronomer-postgres", "astronomer-redis"} {
		if renderedDocExists(docs, "NetworkPolicy", absent) {
			t.Fatalf("production render should not include bundled %s NetworkPolicy when bundled Postgres/Redis are disabled", absent)
		}
	}
}

func TestPreflightNetworkPolicyHookIsDeterministicAndLeastPrivilege(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	policy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-preflight")
	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight")
	if got := stringAt(policy, "metadata", "namespace"); got != "default" {
		t.Fatalf("preflight NetworkPolicy namespace = %q, want rendered release namespace default", got)
	}

	annotations := nestedMap(policy, "metadata", "annotations")
	if got := stringValue(annotations["helm.sh/hook"]); got != "pre-install,pre-upgrade" {
		t.Fatalf("preflight NetworkPolicy hook = %q, want pre-install,pre-upgrade", got)
	}
	policyWeight, err := strconv.Atoi(stringValue(annotations["helm.sh/hook-weight"]))
	if err != nil || policyWeight != -10 {
		t.Fatalf("preflight NetworkPolicy weight = %q, want -10", stringValue(annotations["helm.sh/hook-weight"]))
	}
	if got := stringValue(annotations["helm.sh/hook-delete-policy"]); got != "before-hook-creation" {
		t.Fatalf("preflight NetworkPolicy delete policy = %q, want before-hook-creation", got)
	}
	jobWeight, err := strconv.Atoi(stringValue(nestedMap(job, "metadata", "annotations")["helm.sh/hook-weight"]))
	if err != nil {
		t.Fatalf("parse preflight Job weight: %v", err)
	}
	if policyWeight >= jobWeight {
		t.Fatalf("preflight NetworkPolicy weight %d must be before Job weight %d", policyWeight, jobWeight)
	}

	selector := nestedMap(policy, "spec", "podSelector", "matchLabels")
	wantSelector := map[string]any{
		"app.kubernetes.io/name":      "astronomer",
		"app.kubernetes.io/instance":  "astronomer",
		"app.kubernetes.io/component": "preflight",
	}
	if !reflect.DeepEqual(selector, wantSelector) {
		t.Fatalf("preflight NetworkPolicy selector = %#v, want exactly %#v", selector, wantSelector)
	}
	rawTypes, _ := nestedMap(policy, "spec")["policyTypes"].([]any)
	if got := stringListValue(rawTypes); !reflect.DeepEqual(got, []string{"Egress"}) {
		t.Fatalf("preflight NetworkPolicy policyTypes = %v, want exactly Egress", got)
	}

	// Bundled Postgres has no DB init-container, so the dev policy contains
	// only portable DNS and API access inherited from legacy dev CIDRs.
	wantEgress := []string{
		"*:UDP/53,TCP/53",
		"0.0.0.0/0:TCP/443,TCP/6443",
	}
	if got := preflightEgressContracts(t, policy); !reflect.DeepEqual(got, wantEgress) {
		t.Fatalf("default preflight egress = %v, want exactly %v", got, wantEgress)
	}
}

func TestPreflightNetworkPolicyExternalPostgresAndCIDRUnion(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"postgres.bundled.enabled=false",
		"postgres.port=6432",
		"postgres.external.dsn=postgres://user:password@db.example.invalid:6432/astronomer?sslmode=require",
		"networkPolicy.externalEgressCIDRs[0]=10.10.0.0/16",
		"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
		"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/16",
	))
	policy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-preflight")
	want := []string{
		"*:UDP/53,TCP/53",
		"10.20.0.0/16:TCP/6432",
		"10.10.0.0/16:TCP/6432",
		"10.40.0.0/16:TCP/443,TCP/6443",
		"10.10.0.0/16:TCP/443,TCP/6443",
	}
	got := preflightEgressContracts(t, policy)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("external-Postgres preflight egress = %v, want exactly %v", got, want)
	}
	for _, forbidden := range []string{"TCP/80", "TCP/5432", "TCP/6379", "UDP/443", "TCP/6432,TCP/443"} {
		if strings.Contains(strings.Join(got, "\n"), forbidden) {
			t.Fatalf("rendered policy unexpectedly includes forbidden/general egress %q", forbidden)
		}
	}
}

func TestPreflightNetworkPolicyDisabledModes(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  string
	}{
		{name: "network policy disabled", set: "networkPolicy.enabled=false"},
		{name: "default deny disabled", set: "networkPolicy.defaultDeny=false"},
		{name: "preflight disabled", set: "preflight.enabled=false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t, tt.set))
			if renderedDocExists(docs, "NetworkPolicy", "astronomer-preflight") {
				t.Fatal("preflight NetworkPolicy rendered when its selecting/default-deny contract is disabled")
			}
		})
	}
}

func TestPreflightNetworkPolicyDoesNotInventAPIFallback(t *testing.T) {
	valuesPath := filepath.Join(t.TempDir(), "no-preflight-api-cidrs.yaml")
	values := []byte("networkPolicy:\n  externalEgressCIDRs: []\n  kubernetesAPIEgressCIDRs: []\n")
	if err := os.WriteFile(valuesPath, values, 0o600); err != nil {
		t.Fatalf("write values override: %v", err)
	}
	docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, []string{valuesPath}))
	policy := findRenderedDoc(t, docs, "NetworkPolicy", "astronomer-preflight")
	want := []string{"*:UDP/53,TCP/53"}
	if got := preflightEgressContracts(t, policy); !reflect.DeepEqual(got, want) {
		t.Fatalf("preflight policy invented fallback egress: got %v, want %v", got, want)
	}
}

func preflightEgressContracts(t *testing.T, policy renderedDoc) []string {
	t.Helper()
	rawRules, ok := nestedMap(policy, "spec")["egress"].([]any)
	if !ok {
		t.Fatalf("%s egress is missing or malformed", stringAt(policy, "metadata", "name"))
	}
	result := make([]string, 0, len(rawRules))
	for i, rawRule := range rawRules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			t.Fatalf("egress rule %d is malformed: %#v", i, rawRule)
		}
		cidr := "*"
		if rawTo, exists := rule["to"]; exists {
			to, ok := rawTo.([]any)
			if !ok || len(to) != 1 {
				t.Fatalf("egress rule %d to = %#v, want one ipBlock", i, rawTo)
			}
			destination, _ := to[0].(map[string]any)
			cidr = stringValue(nestedMap(destination, "ipBlock")["cidr"])
			if cidr == "" {
				t.Fatalf("egress rule %d has no CIDR", i)
			}
		}
		rawPorts, ok := rule["ports"].([]any)
		if !ok || len(rawPorts) == 0 {
			t.Fatalf("egress rule %d ports = %#v", i, rule["ports"])
		}
		ports := make([]string, 0, len(rawPorts))
		for _, rawPort := range rawPorts {
			port, _ := rawPort.(map[string]any)
			ports = append(ports, fmt.Sprintf("%s/%v", stringValue(port["protocol"]), port["port"]))
		}
		result = append(result, cidr+":"+strings.Join(ports, ","))
	}
	return result
}

func assertPolicyTypes(t *testing.T, policy renderedDoc, wants ...string) {
	t.Helper()
	rawTypes, _ := nestedMap(policy, "spec")["policyTypes"].([]any)
	got := map[string]bool{}
	for _, raw := range rawTypes {
		got[stringValue(raw)] = true
	}
	for _, want := range wants {
		if !got[want] {
			t.Fatalf("%s missing policyType %q", stringAt(policy, "metadata", "name"), want)
		}
	}
}

func renderedDocExists(docs []renderedDoc, kind, name string) bool {
	for _, doc := range docs {
		if stringValue(doc["kind"]) == kind && stringAt(doc, "metadata", "name") == name {
			return true
		}
	}
	return false
}
