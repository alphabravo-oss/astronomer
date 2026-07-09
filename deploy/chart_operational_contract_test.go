package deploy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	"dex.clientSecret=prod-dex-client-secret",
	"networkPolicy.externalPostgresEgressCIDRs[0]=10.20.0.0/16",
	"networkPolicy.externalRedisEgressCIDRs[0]=10.30.0.0/16",
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

func TestChartHooksAreLimitedToShortLivedJobs(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	allowedHooks := map[string]string{
		"Job/astronomer-migrate":   "post-install,post-upgrade",
		"Job/astronomer-preflight": "pre-install,pre-upgrade",
		// The preflight Job needs its own SA + RBAC created BEFORE it (earlier
		// hook-weight) so a fresh install doesn't deadlock on the main SA not
		// existing yet — see templates/preflight-rbac.yaml. These are the only
		// non-Job hook resources the chart is allowed to ship.
		"ServiceAccount/astronomer-preflight":     "pre-install,pre-upgrade",
		"ClusterRole/astronomer-preflight":        "pre-install,pre-upgrade",
		"ClusterRoleBinding/astronomer-preflight": "pre-install,pre-upgrade",
		"Role/astronomer-preflight":               "pre-install,pre-upgrade",
		"RoleBinding/astronomer-preflight":        "pre-install,pre-upgrade",
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
			t.Fatalf("only short-lived migration/preflight Jobs may use Helm hooks; found hook on %s", key)
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

	// The chart renders the canonical name.
	chartSecret, err := os.ReadFile(filepath.Join(root, "deploy", "chart", "templates", "secret.yaml"))
	if err != nil {
		t.Fatalf("read chart secret.yaml: %v", err)
	}
	if !strings.Contains(string(chartSecret), "ASTRONOMER_ENCRYPTION_KEY:") {
		t.Fatal("chart templates/secret.yaml no longer renders ASTRONOMER_ENCRYPTION_KEY")
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
