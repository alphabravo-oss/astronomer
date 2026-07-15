package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// repoRoot returns the repository root relative to this test file
// (deploy/ sits directly under the root).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Dir(filepath.Dir(here))
}

// productionWiringSets is the minimal --set list that satisfies every
// production preflight/schema gate EXCEPT management backups, so individual
// tests can toggle the backup knobs. Mirrors TestValuesSchemaAcceptsProductionWiring.
var productionWiringSets = []string{
	"config.serverURL=https://astronomer.example.com",
	"gateway.hosts[0]=astronomer.example.com",
	"tls.source=secret",
	"tls.secretName=astronomer-tls",
	"postgres.external.dsnSecretRef.name=astronomer-postgres-dsn",
	"redis.external.address=redis.astronomer.svc.cluster.local:6379",
	"secrets.secretKey=prod-jwt-signing-key",
	"secrets.encryptionKey=prod-fernet-key",
	"bootstrap.email=admin@example.com",
	// F8: production requires a pinned bootstrap password (or existingSecret).
	"bootstrap.password=prod-admin-initial",
	"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
	"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
	// This one CIDR intentionally covers both the example 10.43.0.1 Service
	// address and 10.40.x API endpoint network. Cardinality cannot prove that;
	// operators must inventory the target cluster's actual addresses.
	"networkPolicy.kubernetesAPIEgressCIDRs[0]=10.40.0.0/14",
}

func TestEnterpriseProductionRenderCoversProductionWiringContract(t *testing.T) {
	scriptPath := filepath.Join(repoRoot(t), "scripts", "verify-enterprise.sh")
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read enterprise verifier: %v", err)
	}
	script := string(raw)
	start := strings.Index(script, `step "Fully wired production Helm render"`)
	end := strings.Index(script, `step "Helm chart contract tests"`)
	if start < 0 || end <= start {
		t.Fatal("enterprise verifier production Helm render block is missing or malformed")
	}
	productionBlock := script[start:end]
	arrayIndex := regexp.MustCompile(`\[[0-9]+\]`)
	for _, set := range productionWiringSets {
		key := strings.SplitN(set, "=", 2)[0]
		canonicalKey := arrayIndex.ReplaceAllString(key, "")
		if !strings.Contains(productionBlock, canonicalKey+"=") {
			t.Errorf("enterprise verifier production render is missing productionWiringSets key %q", canonicalKey)
		}
	}
}

type renderedDoc map[string]any

func parseRenderedDocs(t *testing.T, out string) []renderedDoc {
	t.Helper()
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(out), 4096)
	var docs []renderedDoc
	for {
		var doc renderedDoc
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if len(doc) == 0 || doc["kind"] == nil {
			continue
		}
		docs = append(docs, doc)
	}
	return docs
}

func TestChartHooksAreLimitedToLifecycleJobsAndPreflightPrerequisites(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	allowedHooks := map[string]string{
		"Job/astronomer-migrate":          "post-install,post-upgrade",
		"Job/astronomer-preflight":        "pre-install,pre-upgrade",
		"Job/astronomer-preflight-argocd": "test",
		// The preflight Job needs its own SA + RBAC created BEFORE it (earlier
		// hook-weight) so a fresh install doesn't deadlock on the main SA not
		// existing yet — see templates/preflight-rbac.yaml. The matching hook
		// NetworkPolicy restores only the preflight pod's required egress under
		// retained default deny. These are the only non-Job hook resources the
		// chart is allowed to ship.
		"ServiceAccount/astronomer-preflight":     "pre-install,pre-upgrade",
		"ClusterRole/astronomer-preflight":        "pre-install,pre-upgrade",
		"ClusterRoleBinding/astronomer-preflight": "pre-install,pre-upgrade",
		"Role/astronomer-preflight":               "pre-install,pre-upgrade",
		"RoleBinding/astronomer-preflight":        "pre-install,pre-upgrade",
		"NetworkPolicy/astronomer-preflight":      "pre-install,pre-upgrade",
	}
	seen := map[string]bool{}

	for _, doc := range docs {
		annotations := nestedMap(doc, "metadata", "annotations")
		if annotations == nil {
			continue
		}
		hook := stringValue(annotations["helm.sh/hook"])
		if hook == "" {
			continue
		}
		name := stringAt(doc, "metadata", "name")
		// The bundled astro-argocd subchart vendors upstream ArgoCD, which ships
		// its own pre-install hooks (e.g. redis-secret-init); those are outside
		// this chart's control and are not the astronomer lifecycle Jobs this
		// contract guards.
		if strings.HasPrefix(name, "astro-argocd-") {
			continue
		}
		key := fmt.Sprintf("%s/%s", stringValue(doc["kind"]), name)
		want, ok := allowedHooks[key]
		if !ok {
			t.Fatalf("only lifecycle Jobs and dedicated preflight prerequisites may use Helm hooks; found hook on %s", key)
		}
		if hook != want {
			t.Fatalf("%s hook mismatch: got %q, want %q", key, hook, want)
		}
		seen[key] = true
	}

	for key := range allowedHooks {
		if !seen[key] {
			t.Fatalf("expected Helm hook resource %s was not rendered", key)
		}
	}
}

func TestPreflightOwnershipAndOrderingAreIsolatedByDeploymentEngine(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	argoConfig := findRenderedDoc(t, docs, "ConfigMap", "argocd-cm")
	argoConfigData := nestedMap(argoConfig, "data")
	if globalIgnore, found := argoConfigData["resource.customizations.ignoreResourceUpdates.all"]; found {
		t.Fatalf("Argo global status-update suppression = %q, want key omitted so hook Job completion is observed", stringValue(globalIgnore))
	}

	for _, target := range []struct {
		kind string
		name string
	}{
		{kind: "ServiceAccount", name: "astronomer-preflight"},
		{kind: "ClusterRole", name: "astronomer-preflight"},
		{kind: "ClusterRoleBinding", name: "astronomer-preflight"},
		{kind: "Role", name: "astronomer-preflight"},
		{kind: "RoleBinding", name: "astronomer-preflight"},
		{kind: "NetworkPolicy", name: "astronomer-preflight"},
		{kind: "Job", name: "astronomer-preflight"},
	} {
		doc := findRenderedDoc(t, docs, target.kind, target.name)
		annotations := nestedMap(doc, "metadata", "annotations")
		if got := stringValue(annotations["argocd.argoproj.io/hook"]); got != "Skip" {
			t.Errorf("Helm-owned %s/%s Argo hook = %q, want Skip", target.kind, target.name, got)
		}
	}

	for _, target := range []struct {
		kind string
		name string
	}{
		{kind: "ServiceAccount", name: "astronomer-preflight-argocd"},
		{kind: "ClusterRole", name: "astronomer-preflight-argocd"},
		{kind: "ClusterRoleBinding", name: "astronomer-preflight-argocd"},
		{kind: "Role", name: "astronomer-preflight-argocd"},
		{kind: "RoleBinding", name: "astronomer-preflight-argocd"},
		{kind: "NetworkPolicy", name: "astronomer-preflight-argocd"},
	} {
		doc := findRenderedDoc(t, docs, target.kind, target.name)
		annotations := nestedMap(doc, "metadata", "annotations")
		if got := stringValue(annotations["argocd.argoproj.io/sync-wave"]); got != "-5" {
			t.Errorf("Argo prerequisite %s/%s wave = %q, want -5 (same wave as Job)", target.kind, target.name, got)
		}
		for _, forbidden := range []string{"helm.sh/hook", "argocd.argoproj.io/hook", "argocd.argoproj.io/hook-delete-policy"} {
			if got := stringValue(annotations[forbidden]); got != "" {
				t.Errorf("Argo prerequisite %s/%s has lifecycle annotation %s=%q", target.kind, target.name, forbidden, got)
			}
		}
	}

	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight-argocd")
	annotations := nestedMap(job, "metadata", "annotations")
	wantAnnotations := map[string]string{
		"helm.sh/hook":                          "test",
		"argocd.argoproj.io/hook":               "Sync",
		"argocd.argoproj.io/hook-delete-policy": "BeforeHookCreation,HookSucceeded",
		"argocd.argoproj.io/sync-wave":          "-5",
	}
	for key, want := range wantAnnotations {
		if got := stringValue(annotations[key]); got != want {
			t.Errorf("Argo preflight Job %s = %q, want %q", key, got, want)
		}
	}
	if got := stringAt(podSpecFor(job), "serviceAccountName"); got != "astronomer-preflight-argocd" {
		t.Errorf("Argo preflight Job serviceAccountName = %q, want astronomer-preflight-argocd", got)
	}
}

func TestPreflightHookRBACSurvivesUntilJobAndIsLeastPrivilege(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight")
	jobAnnotations := nestedMap(job, "metadata", "annotations")
	if got := stringValue(jobAnnotations["helm.sh/hook"]); got != "pre-install,pre-upgrade" {
		t.Fatalf("preflight Job hook = %q, want pre-install,pre-upgrade", got)
	}
	if got := stringValue(jobAnnotations["helm.sh/hook-weight"]); got != "-5" {
		t.Fatalf("preflight Job hook weight = %q, want -5", got)
	}
	jobWeight, err := strconv.Atoi(stringValue(jobAnnotations["helm.sh/hook-weight"]))
	if err != nil {
		t.Fatalf("parse preflight Job hook weight: %v", err)
	}
	if got := stringValue(jobAnnotations["helm.sh/hook-delete-policy"]); got != "before-hook-creation" {
		t.Fatalf("preflight Job delete policy = %q, want before-hook-creation", got)
	}
	if got := stringAt(podSpecFor(job), "serviceAccountName"); got != "astronomer-preflight" {
		t.Fatalf("preflight Job serviceAccountName = %q, want astronomer-preflight", got)
	}

	for _, target := range []struct {
		kind string
		name string
	}{
		{kind: "ServiceAccount", name: "astronomer-preflight"},
		{kind: "ClusterRole", name: "astronomer-preflight"},
		{kind: "ClusterRoleBinding", name: "astronomer-preflight"},
		{kind: "Role", name: "astronomer-preflight"},
		{kind: "RoleBinding", name: "astronomer-preflight"},
	} {
		doc := findRenderedDoc(t, docs, target.kind, target.name)
		annotations := nestedMap(doc, "metadata", "annotations")
		if got := stringValue(annotations["helm.sh/hook"]); got != "pre-install,pre-upgrade" {
			t.Errorf("%s/%s hook = %q, want pre-install,pre-upgrade", target.kind, target.name, got)
		}
		if got := stringValue(annotations["helm.sh/hook-weight"]); got != "-10" {
			t.Errorf("%s/%s hook weight = %q, want -10", target.kind, target.name, got)
		}
		prerequisiteWeight, err := strconv.Atoi(stringValue(annotations["helm.sh/hook-weight"]))
		if err != nil {
			t.Errorf("parse %s/%s hook weight: %v", target.kind, target.name, err)
		} else if prerequisiteWeight >= jobWeight {
			t.Errorf("%s/%s hook weight %d must run before Job weight %d", target.kind, target.name, prerequisiteWeight, jobWeight)
		}
		policy := stringValue(annotations["helm.sh/hook-delete-policy"])
		if policy != "before-hook-creation" {
			t.Errorf("%s/%s delete policy = %q, want exactly before-hook-creation", target.kind, target.name, policy)
		}
		if strings.Contains(policy, "hook-succeeded") || strings.Contains(policy, "hook-failed") {
			t.Errorf("%s/%s policy %q can delete prerequisite RBAC before the Job runs", target.kind, target.name, policy)
		}
	}

	clusterRole := findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight")
	assertExactRBACRules(t, clusterRole, []rbacRuleContract{
		{apiGroups: []string{"apiextensions.k8s.io"}, resources: []string{"customresourcedefinitions"}, resourceNames: []string{"gateways.gateway.networking.k8s.io", "httproutes.gateway.networking.k8s.io"}, verbs: []string{"get"}},
		{apiGroups: []string{"gateway.networking.k8s.io"}, resources: []string{"gatewayclasses"}, resourceNames: []string{"nginx"}, verbs: []string{"get"}},
	})
	role := findRenderedDoc(t, docs, "Role", "astronomer-preflight")
	if got := stringAt(role, "metadata", "namespace"); got != "default" {
		t.Fatalf("preflight Role namespace = %q, want rendered release namespace default", got)
	}
	assertExactRBACRules(t, role, nil)
	assertExactPreflightBinding(t, findRenderedDoc(t, docs, "ClusterRoleBinding", "astronomer-preflight"), "ClusterRole")
	assertExactPreflightBinding(t, findRenderedDoc(t, docs, "RoleBinding", "astronomer-preflight"), "Role")
}

func TestPreflightRBACResourceNamesFollowRenderedChecks(t *testing.T) {
	t.Run("cert-manager only", func(t *testing.T) {
		docs := parseRenderedDocs(t, helmTemplate(t, "gateway.enabled=false", "tls.source=selfSigned"))
		assertExactRBACRules(t, findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight"), []rbacRuleContract{
			{apiGroups: []string{"apiextensions.k8s.io"}, resources: []string{"customresourcedefinitions"}, resourceNames: []string{"issuers.cert-manager.io", "certificates.cert-manager.io"}, verbs: []string{"get"}},
		})
		assertExactRBACRules(t, findRenderedDoc(t, docs, "Role", "astronomer-preflight"), nil)
	})

	t.Run("cert-manager explicit opt out", func(t *testing.T) {
		sets := []string{"gateway.enabled=false", "tls.source=selfSigned", "tls.requireCertManager=false"}
		docs := parseRenderedDocs(t, helmTemplate(t, sets...))
		assertExactRBACRules(t, findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight"), nil)
		assertExactRBACRules(t, findRenderedDoc(t, docs, "Role", "astronomer-preflight"), nil)
		script := renderedPreflightScript(t, nil, sets...)
		if strings.Contains(script, `kube_read "cert-manager CRD`) {
			t.Fatal("tls.requireCertManager=false rendered cert-manager API reads")
		}
	})

	t.Run("production references and external PVC", func(t *testing.T) {
		prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")
		sets := append([]string{}, productionWiringSets...)
		sets = append(sets,
			"managementBackup.enabled=false",
			"tls.additionalTrustedCAs.enabled=true",
			"tls.additionalTrustedCAs.existingSecret=trusted-ca",
		)
		docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, []string{prodValues}, sets...))
		assertExactRBACRules(t, findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight"), []rbacRuleContract{
			{apiGroups: []string{"apiextensions.k8s.io"}, resources: []string{"customresourcedefinitions"}, resourceNames: []string{"gateways.gateway.networking.k8s.io", "httproutes.gateway.networking.k8s.io"}, verbs: []string{"get"}},
			{apiGroups: []string{"gateway.networking.k8s.io"}, resources: []string{"gatewayclasses"}, resourceNames: []string{"nginx"}, verbs: []string{"get"}},
		})
		assertExactRBACRules(t, findRenderedDoc(t, docs, "Role", "astronomer-preflight"), []rbacRuleContract{
			{apiGroups: []string{""}, resources: []string{"secrets"}, resourceNames: []string{"trusted-ca", "astronomer-postgres-dsn"}, verbs: []string{"get"}},
			{apiGroups: []string{""}, resources: []string{"persistentvolumeclaims"}, resourceNames: []string{"data-astronomer-postgres-0"}, verbs: []string{"get"}},
			{apiGroups: []string{""}, resources: []string{"configmaps"}, resourceNames: []string{"astronomer-dex-config"}, verbs: []string{"get"}},
		})
	})

	t.Run("no live reads", func(t *testing.T) {
		docs := parseRenderedDocs(t, helmTemplate(t, "gateway.enabled=false", "tls.source=none"))
		assertExactRBACRules(t, findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight"), nil)
		assertExactRBACRules(t, findRenderedDoc(t, docs, "Role", "astronomer-preflight"), nil)
	})

	t.Run("Dex cutover exact Secret and legacy ConfigMap", func(t *testing.T) {
		docs := parseRenderedDocs(t, helmTemplate(t,
			"gateway.enabled=false", "tls.source=none", "dex.enabled=true",
			"dex.migration.phase=cutover", "dex.runtimeSecretName=dex-runtime-contract"))
		assertExactRBACRules(t, findRenderedDoc(t, docs, "ClusterRole", "astronomer-preflight"), nil)
		assertExactRBACRules(t, findRenderedDoc(t, docs, "Role", "astronomer-preflight"), []rbacRuleContract{
			{apiGroups: []string{""}, resources: []string{"secrets"}, resourceNames: []string{"dex-runtime-contract"}, verbs: []string{"get"}},
			{apiGroups: []string{""}, resources: []string{"configmaps"}, resourceNames: []string{"astronomer-dex-config-retained"}, verbs: []string{"get"}},
		})
	})
}

func TestPreflightHookUpgradeReplacementContract(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	for _, kind := range []string{"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding"} {
		doc := findRenderedDoc(t, docs, kind, "astronomer-preflight")
		annotations := nestedMap(doc, "metadata", "annotations")
		// Stable names plus both lifecycle events let Helm remove any RBAC left by
		// the current release and recreate it from the upgrading chart. The exact
		// policy is critical: adding hook-succeeded reintroduces the race with the
		// later-weighted Job.
		if got := stringValue(annotations["helm.sh/hook"]); got != "pre-install,pre-upgrade" {
			t.Errorf("%s upgrade hook = %q, want pre-install,pre-upgrade", kind, got)
		}
		if got := stringValue(annotations["helm.sh/hook-delete-policy"]); got != "before-hook-creation" {
			t.Errorf("%s upgrade replacement policy = %q, want before-hook-creation", kind, got)
		}
	}

	disabledDocs := parseRenderedDocs(t, helmTemplate(t, "preflight.enabled=false"))
	for _, kind := range []string{"Job", "ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "NetworkPolicy"} {
		for _, doc := range disabledDocs {
			if stringValue(doc["kind"]) == kind && stringAt(doc, "metadata", "name") == "astronomer-preflight" {
				t.Errorf("preflight.enabled=false unexpectedly rendered %s/astronomer-preflight", kind)
			}
		}
	}
}

func TestPreflightKubernetesReadsUseBoundedExplicitSemantics(t *testing.T) {
	defaultScript := renderedPreflightScript(t, nil)
	for _, want := range []string{
		"PREFLIGHT_READ_ATTEMPTS=10",
		"PREFLIGHT_READ_DELAY_SECONDS=1",
		"kube_read()",
		"return 0",
		"return 1",
		"return 2",
		"Kubernetes API read not ready",
		"Check preflight ServiceAccount RBAC propagation",
	} {
		if !strings.Contains(defaultScript, want) {
			t.Fatalf("preflight script missing bounded-read contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"kubectl get crd gateways.gateway.networking.k8s.io >/dev/null 2>&1",
		"kubectl get pvc \"${legacy_pvc}\" -n \"default\" >/dev/null 2>&1",
		"2>/dev/null | base64 -d 2>/dev/null",
	} {
		if strings.Contains(defaultScript, forbidden) {
			t.Fatalf("preflight script still suppresses kubectl diagnostics via %q", forbidden)
		}
	}

	certScript := renderedPreflightScript(t, nil, "gateway.enabled=false", "tls.source=selfSigned")
	for _, want := range []string{
		`kube_read "cert-manager CRD issuers.cert-manager.io"`,
		`kube_read "cert-manager CRD certificates.cert-manager.io"`,
	} {
		if !strings.Contains(certScript, want) {
			t.Fatalf("cert-manager preflight does not use generic bounded read: missing %q", want)
		}
	}

	prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")
	prodSets := append([]string{}, productionWiringSets...)
	prodSets = append(prodSets,
		"managementBackup.enabled=false",
		"tls.additionalTrustedCAs.enabled=true",
		"tls.additionalTrustedCAs.existingSecret=trusted-ca",
	)
	prodScript := renderedPreflightScript(t, []string{prodValues}, prodSets...)
	for _, want := range []string{
		`kube_read "additional trusted CA Secret trusted-ca"`,
		`kube_read "Postgres DSN Secret ${dsn_secret} key ${dsn_key}"`,
		`kube_read "legacy bundled Postgres PVC ${legacy_pvc}"`,
	} {
		if !strings.Contains(prodScript, want) {
			t.Fatalf("production preflight does not use generic bounded read: missing %q", want)
		}
	}
	if strings.Contains(prodScript, "set -x") {
		t.Fatal("preflight script must not enable shell tracing around DSN material")
	}
}

func TestPreflightBoundedReadRuntimeScenarios(t *testing.T) {
	prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")
	prodSets := append([]string{}, productionWiringSets...)
	prodSets = append(prodSets, "managementBackup.enabled=false", "dex.enabled=false", "config.auth.localPasswordOnly=true")

	tests := []struct {
		name        string
		valueFiles  []string
		sets        []string
		mode        string
		wantSuccess bool
		wantText    string
		wantCalls   int
	}{
		{
			name:        "authorization propagation eventually succeeds",
			mode:        "eventual",
			wantSuccess: true,
			wantText:    "Gateway API prerequisites found.",
			wantCalls:   5,
		},
		{
			name:      "permanent forbidden fails closed after bound",
			mode:      "forbidden",
			wantText:  "Kubernetes API read failed for Gateway API CRD gateways.gateway.networking.k8s.io after 10 attempts.",
			wantCalls: 10,
		},
		{
			name:      "transport failure fails closed after bound",
			mode:      "transport",
			wantText:  "Unable to connect to the server",
			wantCalls: 10,
		},
		{
			name:      "actual missing Gateway CRD is terminal absence",
			mode:      "gateway-missing",
			wantText:  "Gateway API CRD gateways.gateway.networking.k8s.io is missing.",
			wantCalls: 1,
		},
		{
			name:      "actual missing cert-manager CRD",
			sets:      []string{"gateway.enabled=false", "tls.source=selfSigned"},
			mode:      "cert-missing",
			wantText:  "requires cert-manager",
			wantCalls: 1,
		},
		{
			name:      "trusted CA Secret forbidden is not missing",
			sets:      []string{"gateway.enabled=false", "tls.source=none", "tls.additionalTrustedCAs.enabled=true", "tls.additionalTrustedCAs.existingSecret=trusted-ca"},
			mode:      "trusted-forbidden",
			wantText:  "could not verify additional trusted CA Secret trusted-ca because the Kubernetes API read never became usable.",
			wantCalls: 10,
		},
		{
			name:        "legacy PVC genuine absence is allowed",
			sets:        []string{"gateway.enabled=false", "tls.source=none", "postgres.bundled.enabled=false", "postgres.external.dsn=postgres://user:password@db.example.invalid/astronomer?sslmode=require"},
			mode:        "legacy-missing",
			wantSuccess: true,
			wantText:    "No legacy bundled Postgres PVC detected; preflight passes.",
			wantCalls:   1,
		},
		{
			name:      "legacy PVC forbidden fails closed",
			sets:      []string{"gateway.enabled=false", "tls.source=none", "postgres.bundled.enabled=false", "postgres.external.dsn=postgres://user:password@db.example.invalid/astronomer?sslmode=require"},
			mode:      "legacy-forbidden",
			wantText:  "could not determine whether legacy bundled Postgres PVC",
			wantCalls: 10,
		},
		{
			name:       "DSN Secret forbidden is not missing",
			valueFiles: []string{prodValues},
			sets:       prodSets,
			mode:       "dsn-forbidden",
			wantText:   "could not read postgres DSN secret/astronomer-postgres-dsn because the Kubernetes API read never became usable.",
			wantCalls:  13,
		},
		{
			name:        "DSN Secret success validates TLS without disclosure",
			valueFiles:  []string{prodValues},
			sets:        prodSets,
			mode:        "dsn-success",
			wantSuccess: true,
			wantText:    "Production Postgres DSN TLS check passed.",
			wantCalls:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := renderedPreflightScript(t, tt.valueFiles, tt.sets...)
			output, calls, err := runRenderedPreflightScript(t, script, tt.mode)
			if tt.wantSuccess && err != nil {
				t.Fatalf("preflight unexpectedly failed: %v\n%s", err, output)
			}
			if !tt.wantSuccess && err == nil {
				t.Fatalf("preflight unexpectedly passed:\n%s", output)
			}
			if !strings.Contains(output, tt.wantText) {
				t.Fatalf("preflight output missing %q:\n%s", tt.wantText, output)
			}
			if calls != tt.wantCalls {
				t.Fatalf("kubectl calls = %d, want %d; output:\n%s", calls, tt.wantCalls, output)
			}
			if strings.Contains(output, "user:password") {
				t.Fatalf("preflight output disclosed DSN material:\n%s", output)
			}
		})
	}
}

func renderedPreflightScript(t *testing.T, valueFiles []string, sets ...string) string {
	t.Helper()
	docs := parseRenderedDocs(t, helmTemplateWithValueFiles(t, valueFiles, sets...))
	job := findRenderedDoc(t, docs, "Job", "astronomer-preflight")
	container := findContainer(t, podSpecFor(job), "containers", "preflight")
	command := stringListValue(container["command"])
	if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-ec" {
		t.Fatalf("preflight command = %v, want /bin/sh -ec <script>", command)
	}
	return command[2]
}

func runRenderedPreflightScript(t *testing.T, script, mode string) (string, int, error) {
	t.Helper()
	binDir := t.TempDir()
	countFile := filepath.Join(binDir, "kubectl-count")
	kubectl := `#!/bin/sh
count=0
if [ -f "$FAKE_KUBECTL_COUNT_FILE" ]; then count=$(cat "$FAKE_KUBECTL_COUNT_FILE"); fi
count=$((count + 1))
printf '%s' "$count" >"$FAKE_KUBECTL_COUNT_FILE"
args="$*"
not_found() { echo "Error from server (NotFound): requested object not found" >&2; exit 1; }
forbidden() { echo "Error from server (Forbidden): serviceaccount astronomer-preflight cannot get requested resource" >&2; exit 1; }
transport() { echo "Unable to connect to the server: dial tcp: connection refused" >&2; exit 1; }
case "$FAKE_KUBECTL_MODE" in
  eventual)
    if [ "$count" -le 2 ]; then forbidden; fi
    ;;
  forbidden) forbidden ;;
  transport) transport ;;
  gateway-missing)
    echo "$args" | grep -q 'get crd gateways.gateway.networking.k8s.io' && not_found
    ;;
  cert-missing)
    echo "$args" | grep -q 'get crd issuers.cert-manager.io' && not_found
    ;;
  trusted-forbidden)
    echo "$args" | grep -q 'get secret trusted-ca' && forbidden
    ;;
  dsn-forbidden)
    echo "$args" | grep -q 'get secret astronomer-postgres-dsn' && forbidden
    ;;
  dsn-success)
    echo "$args" | grep -q 'get pvc data-astronomer-postgres-0' && not_found
    ;;
  legacy-missing)
    echo "$args" | grep -q 'get pvc data-astronomer-postgres-0' && not_found
    ;;
  legacy-forbidden)
    echo "$args" | grep -q 'get pvc data-astronomer-postgres-0' && forbidden
    ;;
esac
case "$args" in
  *jsonpath=*) printf '%s' 'postgres://user:password@db.example.invalid/astronomer?sslmode=require' | base64 ;;
  *) echo 'resource/example' ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectl), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake sleep: %v", err)
	}
	cmd := exec.Command("/bin/sh", "-ec", script)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"FAKE_KUBECTL_MODE="+mode,
		"FAKE_KUBECTL_COUNT_FILE="+countFile,
	)
	output, err := cmd.CombinedOutput()
	calls := 0
	if raw, readErr := os.ReadFile(countFile); readErr == nil {
		calls, _ = strconv.Atoi(string(raw))
	}
	return string(output), calls, err
}

type rbacRuleContract struct {
	apiGroups     []string
	resources     []string
	resourceNames []string
	verbs         []string
}

func assertExactRBACRules(t *testing.T, role renderedDoc, want []rbacRuleContract) {
	t.Helper()
	rawRules, ok := role["rules"].([]any)
	if !ok {
		t.Fatalf("%s/%s rules are missing or malformed", stringValue(role["kind"]), stringAt(role, "metadata", "name"))
	}
	if len(rawRules) != len(want) {
		t.Fatalf("%s/%s has %d rules, want exactly %d", stringValue(role["kind"]), stringAt(role, "metadata", "name"), len(rawRules), len(want))
	}
	for i, rawRule := range rawRules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			t.Fatalf("%s/%s rule %d is malformed", stringValue(role["kind"]), stringAt(role, "metadata", "name"), i)
		}
		expectedFields := map[string][]string{
			"apiGroups":     want[i].apiGroups,
			"resources":     want[i].resources,
			"resourceNames": want[i].resourceNames,
			"verbs":         want[i].verbs,
		}
		for field := range rule {
			if _, allowed := expectedFields[field]; !allowed {
				t.Errorf("%s/%s rule %d has unexpected field %q", stringValue(role["kind"]), stringAt(role, "metadata", "name"), i, field)
			}
		}
		for field, expected := range expectedFields {
			got := stringListValue(rule[field])
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("%s/%s rule %d %s = %v, want exactly %v", stringValue(role["kind"]), stringAt(role, "metadata", "name"), i, field, got, expected)
			}
		}
	}
}

func assertExactPreflightBinding(t *testing.T, binding renderedDoc, roleKind string) {
	t.Helper()
	if roleKind == "Role" {
		if got := stringAt(binding, "metadata", "namespace"); got != "default" {
			t.Errorf("RoleBinding/astronomer-preflight namespace = %q, want rendered release namespace default", got)
		}
	}
	roleRef := nestedMap(binding, "roleRef")
	if got := stringValue(roleRef["apiGroup"]); got != "rbac.authorization.k8s.io" {
		t.Errorf("%s roleRef.apiGroup = %q, want rbac.authorization.k8s.io", stringValue(binding["kind"]), got)
	}
	if got := stringValue(roleRef["kind"]); got != roleKind {
		t.Errorf("%s roleRef.kind = %q, want %q", stringValue(binding["kind"]), got, roleKind)
	}
	if got := stringValue(roleRef["name"]); got != "astronomer-preflight" {
		t.Errorf("%s roleRef.name = %q, want astronomer-preflight", stringValue(binding["kind"]), got)
	}
	rawSubjects, ok := binding["subjects"].([]any)
	if !ok || len(rawSubjects) != 1 {
		t.Fatalf("%s subjects = %v, want exactly one ServiceAccount subject", stringValue(binding["kind"]), binding["subjects"])
	}
	subject, ok := rawSubjects[0].(map[string]any)
	if !ok {
		t.Fatalf("%s subject is malformed", stringValue(binding["kind"]))
	}
	if got := stringValue(subject["kind"]); got != "ServiceAccount" {
		t.Errorf("%s subject kind = %q, want ServiceAccount", stringValue(binding["kind"]), got)
	}
	if got := stringValue(subject["name"]); got != "astronomer-preflight" {
		t.Errorf("%s subject name = %q, want astronomer-preflight", stringValue(binding["kind"]), got)
	}
	if got := stringValue(subject["namespace"]); got != "default" {
		t.Errorf("%s subject namespace = %q, want rendered release namespace default", stringValue(binding["kind"]), got)
	}
}

func stringListValue(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, stringValue(item))
	}
	return result
}

func TestServiceAccountAndRuntimeRBACAreManagedReleaseResources(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	for _, target := range []struct {
		kind string
		name string
	}{
		{kind: "ServiceAccount", name: "astronomer"},
		{kind: "ClusterRole", name: "astronomer"},
		{kind: "ClusterRoleBinding", name: "astronomer"},
	} {
		doc := findRenderedDoc(t, docs, target.kind, target.name)
		annotations := nestedMap(doc, "metadata", "annotations")
		if annotations == nil {
			continue
		}
		for _, forbidden := range []string{"helm.sh/hook", "helm.sh/resource-policy"} {
			if stringValue(annotations[forbidden]) != "" {
				t.Fatalf("%s/%s must be a normal managed release resource, but has %s=%q", target.kind, target.name, forbidden, stringValue(annotations[forbidden]))
			}
		}
	}
}

func TestBootstrapSecretAndServerEnvAreWired(t *testing.T) {
	const bootstrapPassword = "operator-provided-initial-password"
	const bootstrapEmail = "admin@example.com"
	docs := parseRenderedDocs(t, helmTemplate(t,
		"bootstrap.password="+bootstrapPassword,
		"bootstrap.email="+bootstrapEmail,
	))

	secret := findRenderedDoc(t, docs, "Secret", "astronomer-bootstrap")
	stringData := nestedMap(secret, "stringData")
	if got := stringValue(stringData["password"]); got != bootstrapPassword {
		t.Fatalf("bootstrap secret password = %q, want %q", got, bootstrapPassword)
	}

	server := findRenderedDoc(t, docs, "Deployment", "astronomer-server")
	container := findContainer(t, podSpecFor(server), "containers", "server")
	passwordEnv := findEnvVar(t, container, "ASTRONOMER_BOOTSTRAP_PASSWORD")
	ref := nestedMap(passwordEnv, "valueFrom", "secretKeyRef")
	if got := stringValue(ref["name"]); got != "astronomer-bootstrap" {
		t.Fatalf("bootstrap password secret name = %q, want astronomer-bootstrap", got)
	}
	if got := stringValue(ref["key"]); got != "password" {
		t.Fatalf("bootstrap password secret key = %q, want password", got)
	}

	emailEnv := findEnvVar(t, container, "ASTRONOMER_BOOTSTRAP_EMAIL")
	if got := stringValue(emailEnv["value"]); got != bootstrapEmail {
		t.Fatalf("bootstrap email env = %q, want %q", got, bootstrapEmail)
	}
}

func TestRenderedContainersDeclareImagePullPolicy(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.endpoint=http://loki.observability.svc:3100",
		"managementBackup.enabled=true",
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=backup-creds",
		"managementRestoreDrill.enabled=true",
		"dex.enabled=true",
	))

	for _, doc := range docs {
		podSpec := podSpecFor(doc)
		if podSpec == nil {
			continue
		}
		workload := fmt.Sprintf("%s/%s", stringValue(doc["kind"]), stringAt(doc, "metadata", "name"))
		for _, field := range []string{"initContainers", "containers"} {
			for _, container := range containerList(podSpec, field) {
				if stringValue(container["image"]) == "" {
					continue
				}
				if stringValue(container["imagePullPolicy"]) == "" {
					t.Fatalf("%s %s %q does not declare imagePullPolicy", workload, field, stringValue(container["name"]))
				}
			}
		}
	}
}

func TestGlobalImageRegistryAndPullPolicyApplyToCoreImages(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"image.registry=registry.example.com/platform",
		"image.pullPolicy=Always",
		"image.server.tag=v-server",
		"image.worker.tag=v-worker",
		"image.migrate.tag=v-migrate",
		"frontend.image.tag=v-frontend",
		// This test deliberately gives each image a distinct tag to prove the
		// per-image tag plumbing; that trips the F1 server↔migrate skew guard,
		// so opt into the skew explicitly (it is not a real deploy).
		"image.allowSchemaSkew=true",
	))

	assertContainerImage(t, docs, "Deployment", "astronomer-server", "containers", "server", "registry.example.com/platform/astronomer-go-server:v-server", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-server", "initContainers", "migrate", "registry.example.com/platform/astronomer-go-migrate:v-migrate", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-worker", "containers", "worker", "registry.example.com/platform/astronomer-go-worker:v-worker", "Always")
	assertContainerImage(t, docs, "Deployment", "astronomer-frontend", "containers", "frontend", "registry.example.com/platform/astronomer-frontend:v-frontend", "Always")
	assertContainerImage(t, docs, "Job", "astronomer-migrate", "containers", "migrate", "registry.example.com/platform/astronomer-go-migrate:v-migrate", "Always")
	assertContainerImage(t, docs, "StatefulSet", "astronomer-postgres", "containers", "postgres", "registry.example.com/platform/postgres:16-alpine", "Always")
	// The bundled cache engine is Valkey (BSD-licensed Redis fork); the resource
	// stays named "redis" but the image is valkey/valkey (see values.yaml).
	assertContainerImage(t, docs, "StatefulSet", "astronomer-redis", "containers", "redis", "registry.example.com/platform/valkey/valkey:8-alpine", "Always")
}

func assertContainerImage(t *testing.T, docs []renderedDoc, kind, name, field, containerName, wantImage, wantPullPolicy string) {
	t.Helper()
	doc := findRenderedDoc(t, docs, kind, name)
	container := findContainer(t, podSpecFor(doc), field, containerName)
	if got := stringValue(container["image"]); got != wantImage {
		t.Fatalf("%s/%s %s %q image = %q, want %q", kind, name, field, containerName, got, wantImage)
	}
	if got := stringValue(container["imagePullPolicy"]); got != wantPullPolicy {
		t.Fatalf("%s/%s %s %q imagePullPolicy = %q, want %q", kind, name, field, containerName, got, wantPullPolicy)
	}
}

func findRenderedDoc(t *testing.T, docs []renderedDoc, kind, name string) renderedDoc {
	t.Helper()
	for _, doc := range docs {
		if stringValue(doc["kind"]) == kind && stringAt(doc, "metadata", "name") == name {
			return doc
		}
	}
	t.Fatalf("rendered %s/%s not found", kind, name)
	return nil
}

func podSpecFor(doc renderedDoc) map[string]any {
	switch stringValue(doc["kind"]) {
	case "Deployment", "StatefulSet", "DaemonSet":
		return nestedMap(doc, "spec", "template", "spec")
	case "Job":
		return nestedMap(doc, "spec", "template", "spec")
	case "CronJob":
		return nestedMap(doc, "spec", "jobTemplate", "spec", "template", "spec")
	default:
		return nil
	}
}

func findContainer(t *testing.T, podSpec map[string]any, field, name string) map[string]any {
	t.Helper()
	for _, container := range containerList(podSpec, field) {
		if stringValue(container["name"]) == name {
			return container
		}
	}
	t.Fatalf("%s container %q not found", field, name)
	return nil
}

func containerList(podSpec map[string]any, field string) []map[string]any {
	if podSpec == nil {
		return nil
	}
	rawList, _ := podSpec[field].([]any)
	containers := make([]map[string]any, 0, len(rawList))
	for _, raw := range rawList {
		if container, ok := raw.(map[string]any); ok {
			containers = append(containers, container)
		}
	}
	return containers
}

func findEnvVar(t *testing.T, container map[string]any, name string) map[string]any {
	t.Helper()
	rawList, _ := container["env"].([]any)
	for _, raw := range rawList {
		env, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(env["name"]) == name {
			return env
		}
	}
	t.Fatalf("env var %q not found", name)
	return nil
}

func stringAt(root map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}
	cur := root
	for _, key := range path[:len(path)-1] {
		cur = nestedMap(cur, key)
		if cur == nil {
			return ""
		}
	}
	return stringValue(cur[path[len(path)-1]])
}

func nestedMap(root map[string]any, path ...string) map[string]any {
	cur := root
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// O-02: the encryption-key Secret must survive `helm uninstall` or a rebuild
// makes every encrypted column undecryptable (see the DR runbook).
func TestSecretsCarryResourcePolicyKeep(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	secret := findRenderedDoc(t, docs, "Secret", "astronomer-secrets")
	annotations := nestedMap(secret, "metadata", "annotations")
	if annotations == nil {
		t.Fatal("astronomer-secrets has no annotations block; helm.sh/resource-policy=keep is required so the Fernet/JWT keys survive helm uninstall")
	}
	if got := stringValue(annotations["helm.sh/resource-policy"]); got != "keep" {
		t.Fatalf("astronomer-secrets helm.sh/resource-policy = %q, want keep", got)
	}
}

// O-04: production must not silently render without management-plane backups.
func TestProductionRequiresBackupsWired(t *testing.T) {
	prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")

	// Backup enabled (default) but S3 target unset → render must fail and the
	// failure must call out the backup wiring.
	errOut := helmTemplateExpectError(t, []string{prodValues}, productionWiringSets...)
	if !strings.Contains(errOut, "managementBackup") {
		t.Fatalf("production render with unwired backups did not mention managementBackup:\n%s", errOut)
	}

	// Explicit opt-out via enabled=false → render must succeed.
	optOut := append([]string{}, productionWiringSets...)
	optOut = append(optOut, "managementBackup.enabled=false")
	out := helmTemplateWithValueFiles(t, []string{prodValues}, optOut...)
	if strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("managementBackup.enabled=false should not render the backup CronJob:\n%s", out)
	}
}

// OPS-01: production with backups enabled must also require encryption-key
// wrap custody. S3-only wiring leaves CronJobs green but key backup inert —
// a restore onto a new cluster then cannot decrypt Fernet columns.
func TestProductionRequiresKeyWrapWhenBackupsEnabled(t *testing.T) {
	prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")

	// S3 wired, wrapping secret empty → preflight must refuse.
	s3Only := append([]string{}, productionWiringSets...)
	s3Only = append(s3Only,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-aws",
	)
	errOut := helmTemplateExpectError(t, []string{prodValues}, s3Only...)
	if !strings.Contains(errOut, "wrappingSecretRef") && !strings.Contains(errOut, "encryptionKeyBackup") {
		t.Fatalf("production render with S3 but no key wrap did not mention wrappingSecretRef/encryptionKeyBackup:\n%s", errOut)
	}

	// Explicit opt-out of key backup (still with S3) → render must succeed,
	// but the key-backup path stays inert.
	optOut := append([]string{}, s3Only...)
	optOut = append(optOut, "managementBackup.encryptionKeyBackup.enabled=false")
	out := helmTemplateWithValueFiles(t, []string{prodValues}, optOut...)
	if !strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("S3-wired backup CronJob should render when key backup is explicitly disabled:\n%s", out)
	}
	if strings.Contains(out, "- name: KEYBACKUP_ENABLED") {
		t.Fatalf("encryptionKeyBackup.enabled=false must not arm KEYBACKUP_ENABLED:\n%s", out)
	}

	// Full wiring (S3 + wrap) → backup CronJob renders with key-backup armed.
	full := append([]string{}, s3Only...)
	full = append(full, "managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap")
	out = helmTemplateWithValueFiles(t, []string{prodValues}, full...)
	if !strings.Contains(out, "name: astronomer-management-backup") {
		t.Fatalf("fully-wired production backup CronJob missing:\n%s", out)
	}
	if !strings.Contains(out, "- name: KEYBACKUP_ENABLED") {
		t.Fatalf("fully-wired production render should arm key backup:\n%s", out)
	}
	if !strings.Contains(out, `secretName: "astronomer-key-wrap"`) {
		t.Fatalf("fully-wired production render should mount wrapping secret:\n%s", out)
	}
}

// OPS-01: production with backups + S3 wired but no key-wrap secret must fail.
func TestProductionRequiresEncryptionKeyWrapWhenBackupsEnabled(t *testing.T) {
	prodValues := filepath.Join(repoRoot(t), "deploy", "chart", "values-production.yaml")
	sets := append([]string{}, productionWiringSets...)
	sets = append(sets,
		"managementBackup.s3.bucket=astronomer-backups",
		"managementBackup.s3.credentialsSecretRef.name=astronomer-backup-creds",
		// wrap name left empty on purpose
	)
	errOut := helmTemplateExpectError(t, []string{prodValues}, sets...)
	if !strings.Contains(errOut, "wrappingSecretRef") && !strings.Contains(errOut, "encryptionKeyBackup") {
		t.Fatalf("production render with S3 but no key wrap must fail on wrap custody:\n%s", errOut)
	}

	// With wrap wired, production render succeeds.
	okSets := append([]string{}, sets...)
	okSets = append(okSets, "managementBackup.encryptionKeyBackup.wrappingSecretRef.name=astronomer-key-wrap")
	_ = helmTemplateWithValueFiles(t, []string{prodValues}, okSets...)
}

// O-06: the restore-drill schema floor must track the real max migration so a
// stale backup can't pass the drill. Fail if values.yaml is >10 versions behind.
func TestSchemaFloorTracksMaxMigration(t *testing.T) {
	root := repoRoot(t)
	migDir := filepath.Join(root, "internal", "db", "migrations")
	entries, err := os.ReadDir(migDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	verRe := regexp.MustCompile(`^0*(\d+)_.*\.up\.sql$`)
	maxVer := 0
	for _, e := range entries {
		m := verRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if v > maxVer {
			maxVer = v
		}
	}
	if maxVer == 0 {
		t.Fatal("no *.up.sql migrations found")
	}

	values, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	floorRe := regexp.MustCompile(`(?m)^\s*expectedMinSchemaVersion:\s*(\d+)`)
	fm := floorRe.FindStringSubmatch(string(values))
	if fm == nil {
		t.Fatal("expectedMinSchemaVersion not found in values.yaml")
	}
	floor, _ := strconv.Atoi(fm[1])

	const maxLag = 10
	if maxVer-floor > maxLag {
		t.Fatalf("expectedMinSchemaVersion=%d is %d behind the max migration %d (allowed lag %d); bump it in deploy/chart/values.yaml",
			floor, maxVer-floor, maxVer, maxLag)
	}
	if floor > maxVer {
		t.Fatalf("expectedMinSchemaVersion=%d is ahead of the max migration %d", floor, maxVer)
	}
}

// O-01 / R-03 / C-11 / C-05: the encryption-key env var is ASTRONOMER_ENCRYPTION_KEY
// everywhere. Guard against the bare-ENCRYPTION_KEY drift that silently breaks
// decryption (the DR runbook, raw manifests, and docker-compose must not use it;
// keyrotate must accept the canonical name).
func TestEncryptionKeyNameHasNoBareDrift(t *testing.T) {
	root := repoRoot(t)

	// The chart writes the configured canonical key and workloads reference that
	// exact key from the selected existing Secret.
	chartSecret, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", "secret.yaml"))
	if err != nil {
		t.Fatalf("read chart secret.yaml: %v", err)
	}
	if !strings.Contains(string(chartSecret), `.Values.secrets.encryptionKeyKey`) {
		t.Fatal("chart templates/secret.yaml no longer renders the configured encryption key name")
	}
	for _, template := range []string{"server-deployment.yaml", "worker-deployment.yaml"} {
		raw, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", template))
		if err != nil {
			t.Fatalf("read %s: %v", template, err)
		}
		if !strings.Contains(string(raw), "ASTRONOMER_ENCRYPTION_KEY") || !strings.Contains(string(raw), `.Values.secrets.encryptionKeyKey`) {
			t.Fatalf("%s must expose ASTRONOMER_ENCRYPTION_KEY from the configured Secret key", template)
		}
	}

	// The DR runbook must not read/recreate the key under the bare name.
	runbook, err := os.ReadFile(filepath.Join(root, "docs", "management-plane-dr-runbook.md"))
	if err != nil {
		t.Fatalf("read DR runbook: %v", err)
	}
	for _, bad := range []string{"data.ENCRYPTION_KEY", "from-literal=ENCRYPTION_KEY="} {
		if strings.Contains(string(runbook), bad) {
			t.Fatalf("DR runbook still uses bare %q — server/worker read ASTRONOMER_ENCRYPTION_KEY, so this silently no-ops during a real restore", bad)
		}
	}

	// Raw manifests and docker-compose must not assign the bare key.
	bareAssign := regexp.MustCompile(`(?m)^\s+ENCRYPTION_KEY\s*:`)
	for _, rel := range []string{
		filepath.Join("deploy", "k8s", "03-secret.yaml"),
		filepath.Join("deploy", "docker-compose.yml"),
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if bareAssign.Match(b) {
			t.Fatalf("%s assigns bare ENCRYPTION_KEY; rename to ASTRONOMER_ENCRYPTION_KEY", rel)
		}
		if !strings.Contains(string(b), "ASTRONOMER_ENCRYPTION_KEY") {
			t.Fatalf("%s does not set ASTRONOMER_ENCRYPTION_KEY", rel)
		}
	}

	// keyrotate must accept the canonical name (fallback).
	kr, err := os.ReadFile(filepath.Join(root, "cmd", "keyrotate", "main.go"))
	if err != nil {
		t.Fatalf("read keyrotate main.go: %v", err)
	}
	if !strings.Contains(string(kr), "ASTRONOMER_ENCRYPTION_KEY") {
		t.Fatal("cmd/keyrotate/main.go does not read ASTRONOMER_ENCRYPTION_KEY")
	}
}

// C-02: the worker Deployment must ship liveness + readiness probes hitting
// /healthz so a wedged consumer is restarted and rollouts gate on health.
func TestWorkerDeploymentHasProbes(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	worker := findRenderedDoc(t, docs, "Deployment", "astronomer-worker")
	container := findContainer(t, podSpecFor(worker), "containers", "worker")
	for _, probe := range []string{"livenessProbe", "readinessProbe"} {
		httpGet := nestedMap(container, probe, "httpGet")
		if httpGet == nil {
			t.Fatalf("worker container has no %s.httpGet", probe)
		}
		if got := stringValue(httpGet["path"]); got != "/healthz" {
			t.Fatalf("worker %s path = %q, want /healthz", probe, got)
		}
	}

	// Gated behind worker.probes.enabled.
	docsOff := parseRenderedDocs(t, helmTemplate(t, "worker.probes.enabled=false"))
	workerOff := findRenderedDoc(t, docsOff, "Deployment", "astronomer-worker")
	containerOff := findContainer(t, podSpecFor(workerOff), "containers", "worker")
	if _, ok := containerOff["livenessProbe"]; ok {
		t.Fatal("worker.probes.enabled=false should omit the livenessProbe")
	}
}

// O-05 / O-09: every runbook_url the PrometheusRule references must resolve to a
// file under docs/runbooks/, so an alert never points an operator at a 404.
func TestPrometheusRunbookURLsResolve(t *testing.T) {
	root := repoRoot(t)
	rules, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", "prometheus-rules.yaml"))
	if err != nil {
		t.Fatalf("read prometheus-rules.yaml: %v", err)
	}
	// runbook_url: {{ .Values.metrics.prometheusRule.runbookBaseURL }}/<basename>
	re := regexp.MustCompile(`runbookBaseURL\s*}}/([A-Za-z0-9._-]+)`)
	matches := re.FindAllStringSubmatch(string(rules), -1)
	if len(matches) == 0 {
		t.Fatal("no runbook_url references found in prometheus-rules.yaml")
	}
	seen := map[string]bool{}
	for _, m := range matches {
		base := m[1]
		if seen[base] {
			continue
		}
		seen[base] = true
		p := filepath.Join(root, "docs", "runbooks", base)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("runbook_url references %q but docs/runbooks/%s does not exist", base, base)
		}
	}
}
