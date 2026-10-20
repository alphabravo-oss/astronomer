package deploy

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

// Live-cluster compatibility fixtures: the sanitized `helm get values` of the
// astronomer-dev release and the overlay that folds the 2026-10-20 hand-made
// runtime objects into chart values.
var (
	liveValuesFile  = filepath.Join("testdata", "live-values-astronomer-dev.yaml")
	liveOverlayFile = filepath.Join("testdata", "live-overlay-astronomer-dev.yaml")
)

// k3sKubeVersion is the Kubernetes version of the astronomer-dev k3s node.
const k3sKubeVersion = "1.37.0"

// helmTemplateRaw renders deploy/chart with only the chart's own values.yaml
// defaults plus the given value files, exactly like `helm upgrade -f`, and
// returns stdout, stderr and the error.
func helmTemplateRaw(t *testing.T, kubeVersion string, valueFiles []string, sets ...string) (string, string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm binary not on PATH (%v); skipping chart-render test", err)
	}
	args := []string{"template", "astronomer", filepath.Join(repoRoot(t), "deploy", "chart"), "--namespace", "astronomer", "--kube-version", kubeVersion}
	for _, file := range valueFiles {
		args = append(args, "-f", file)
	}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	cmd := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func helmTemplateLive(t *testing.T, valueFiles []string, sets ...string) string {
	t.Helper()
	out, stderr, err := helmTemplateRaw(t, k3sKubeVersion, valueFiles, sets...)
	if err != nil {
		t.Fatalf("helm template failed: %v\nstderr=%s", err, stderr)
	}
	return out
}

func TestChartKubeVersionRangeCoversK3s137(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"1.32.9", false},
		{"1.33.0", true},
		{"1.35.0", true},
		{"1.37.0", true},
		{"1.37.4", true},
		{"1.38.0", false},
	} {
		_, stderr, err := helmTemplateRaw(t, tc.version, []string{filepath.Join("chart", "values-dev.yaml")}, testRenderSecretKeySet, testRenderEncryptionKeySet)
		if tc.ok && err != nil {
			t.Errorf("kube %s rejected: %s", tc.version, stderr)
		}
		if !tc.ok && (err == nil || !strings.Contains(stderr, "kubeVersion")) {
			t.Errorf("kube %s accepted or failed for the wrong reason: %v %s", tc.version, err, stderr)
		}
	}
}

// TestLiveReleaseValuesRenderOnK3s137 proves the chart still renders the live
// astronomer-dev release values verbatim (minus secrets) on the cluster's k3s
// version, alone and with the runtime overlay.
func TestLiveReleaseValuesRenderOnK3s137(t *testing.T) {
	for _, files := range [][]string{{liveValuesFile}, {liveValuesFile, liveOverlayFile}} {
		docs := parseRenderedDocs(t, helmTemplateLive(t, files))
		kinds := map[string]int{}
		for _, doc := range docs {
			kinds[stringValue(doc["kind"])]++
		}
		for _, want := range []string{"Deployment", "Cluster", "ScheduledBackup", "CronJob", "NetworkPolicy", "Ingress"} {
			if kinds[want] == 0 {
				t.Errorf("%v render lacks %s (kinds=%v)", files, want, kinds)
			}
		}
		cluster := findRenderedDoc(t, docs, "Cluster", "astronomer-cnpg")
		if got := stringAt(cluster, "spec", "imageName"); !strings.HasPrefix(got, "ghcr.io/cloudnative-pg/postgresql:17.11-") {
			t.Errorf("live CNPG image drifted: %q", got)
		}
	}
}

func egressRulesTo(policy networkingv1.NetworkPolicy, match func(networkingv1.NetworkPolicyPeer) bool) []networkingv1.NetworkPolicyEgressRule {
	var rules []networkingv1.NetworkPolicyEgressRule
	for _, rule := range policy.Spec.Egress {
		for _, peer := range rule.To {
			if match(peer) {
				rules = append(rules, rule)
				break
			}
		}
	}
	return rules
}

func ipBlockPeer(cidr string) func(networkingv1.NetworkPolicyPeer) bool {
	return func(peer networkingv1.NetworkPolicyPeer) bool {
		return peer.IPBlock != nil && peer.IPBlock.CIDR == cidr
	}
}

func podLabelPeer(key, value string) func(networkingv1.NetworkPolicyPeer) bool {
	return func(peer networkingv1.NetworkPolicyPeer) bool {
		return peer.NamespaceSelector == nil && peer.PodSelector != nil && peer.PodSelector.MatchLabels[key] == value
	}
}

func namespacePeer(name string) func(networkingv1.NetworkPolicyPeer) bool {
	return func(peer networkingv1.NetworkPolicyPeer) bool {
		return peer.NamespaceSelector != nil && peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == name
	}
}

func rulesAllowPorts(rules []networkingv1.NetworkPolicyEgressRule, ports ...int32) bool {
	for _, port := range ports {
		found := false
		for _, rule := range rules {
			if portsContain(rule.Ports, port) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return len(rules) > 0
}

func policyByName(t *testing.T, policies []networkingv1.NetworkPolicy, name string) networkingv1.NetworkPolicy {
	t.Helper()
	for _, policy := range policies {
		if policy.Name == name {
			return policy
		}
	}
	t.Fatalf("render lacks NetworkPolicy %s", name)
	return networkingv1.NetworkPolicy{}
}

// TestCNPGNetworkPolicyCoversHandMadeRuntimePolicies asserts the chart-rendered
// policies admit every flow the hand-made astronomer-dev policies opened:
// astronomer-cnpg-kubeapi-egress, astronomer-offhost-s3-egress,
// astronomer-dr-jobs-db-{ingress,egress} and astronomer-dr-jobs-offhost-s3-egress.
func TestCNPGNetworkPolicyCoversHandMadeRuntimePolicies(t *testing.T) {
	policies := renderedNetworkPolicies(t, helmTemplateLive(t, []string{liveValuesFile, liveOverlayFile}))
	postgres := policyByName(t, policies, "astronomer-postgres")
	if postgres.Spec.PodSelector.MatchLabels["cnpg.io/cluster"] != "astronomer-cnpg" {
		t.Fatalf("postgres policy selects %v", postgres.Spec.PodSelector.MatchLabels)
	}
	for _, cidr := range []string{"10.43.0.1/32", "5.78.179.107/32", "100.86.167.87/32"} {
		if !rulesAllowPorts(egressRulesTo(postgres, ipBlockPeer(cidr)), 443, 6443) {
			t.Errorf("CNPG instance cannot reach kube-apiserver at %s:443/6443", cidr)
		}
	}
	if !rulesAllowPorts(egressRulesTo(postgres, namespacePeer("cnpg-system")), 9090) {
		t.Error("CNPG instance cannot reach the Barman Cloud plugin in cnpg-system:9090")
	}
	if !rulesAllowPorts(egressRulesTo(postgres, ipBlockPeer("100.118.107.29/32")), 9000, 443) {
		t.Error("CNPG instance cannot archive WAL to the off-host RustFS 100.118.107.29:9000")
	}

	admitted := map[string]bool{}
	for _, rule := range postgres.Spec.Ingress {
		if !portsContain(rule.Ports, 5432) {
			continue
		}
		for _, peer := range rule.From {
			if peer.PodSelector != nil {
				admitted[peer.PodSelector.MatchLabels["app.kubernetes.io/component"]] = true
			}
		}
	}
	// management-backup-retention only talks to S3 (no DB client in the job),
	// so it is deliberately not admitted on 5432.
	for _, component := range []string{"management-backup", "restore-drill"} {
		if !admitted[component] {
			t.Errorf("astronomer-postgres does not admit %s on 5432 (admitted=%v)", component, admitted)
		}
		policy := policyByComponent(t, policies, component)
		if !rulesAllowPorts(egressRulesTo(policy, podLabelPeer("cnpg.io/cluster", "astronomer-cnpg")), 5432) {
			t.Errorf("%s has no egress to the CNPG cluster on 5432", component)
		}
	}
	if admitted["management-backup-retention"] {
		t.Error("retention job must not be admitted to Postgres")
	}
	for _, component := range []string{"management-backup", "management-backup-retention", "restore-drill"} {
		policy := policyByComponent(t, policies, component)
		if !rulesAllowPorts(egressRulesTo(policy, ipBlockPeer("100.118.107.29/32")), 9000, 443) {
			t.Errorf("%s cannot reach the S3 endpoint 100.118.107.29:9000", component)
		}
		if rulesAllowPorts(egressRulesTo(policy, ipBlockPeer("100.118.107.29/32")), 80) {
			t.Errorf("%s keeps the default port 80 although objectStoreEgressPorts overrides it", component)
		}
	}
}

func TestCNPGNetworkPolicyDefaultsStayClosed(t *testing.T) {
	out := helmTemplate(t,
		"postgres.mode=cloudNativePG",
		"networkPolicy.externalEgressCIDRs=null",
		"postgres.cloudNativePG.barmanCloud.enabled=false",
	)
	postgres := policyByComponent(t, renderedNetworkPolicies(t, out), "postgres")
	if len(egressRulesTo(postgres, namespacePeer("cnpg-system"))) != 0 {
		t.Error("plugin egress rendered although Barman Cloud is disabled")
	}
	for _, rule := range postgres.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				t.Errorf("CNPG egress opened %s without any CIDR value", peer.IPBlock.CIDR)
			}
		}
	}
	if !slices.ContainsFunc(postgres.Spec.Egress, func(rule networkingv1.NetworkPolicyEgressRule) bool { return portsContain(rule.Ports, 53) }) {
		t.Error("CNPG egress lost DNS")
	}
}

// handMadeOffhostObjectStoreSpec is the spec of the ObjectStore applied by hand
// on astronomer-dev (runtime-changes-20261020/cnpg-backup/objectstore-offhost.yaml).
const handMadeOffhostObjectStoreSpec = `
configuration:
  destinationPath: s3://astronomer-recovery-offhost/
  endpointURL: http://100.118.107.29:9000
  s3Credentials:
    accessKeyId: {name: offhost-rustfs-credentials, key: ACCESS_KEY_ID}
    secretAccessKey: {name: offhost-rustfs-credentials, key: SECRET_ACCESS_KEY}
  data: {compression: gzip, jobs: 2}
  wal: {compression: gzip, maxParallel: 2}
instanceSidecarConfiguration:
  env:
  - {name: AWS_DEFAULT_REGION, value: us-east-1}
  retentionPolicyIntervalSeconds: 1800
retentionPolicy: 14d
`

func TestChartManagedObjectStoreMatchesLiveObjectStore(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplateLive(t, []string{liveValuesFile, liveOverlayFile}))
	store := findRenderedDoc(t, docs, "ObjectStore", "offhost-archive")
	if got := stringAt(store, "apiVersion"); got != "barmancloud.cnpg.io/v1" {
		t.Fatalf("ObjectStore apiVersion = %q", got)
	}
	want := parseRenderedDocs(t, "kind: Spec\nspec:\n"+indent(handMadeOffhostObjectStoreSpec, "  "))[0]["spec"]
	if !reflect.DeepEqual(store["spec"], want) {
		t.Fatalf("chart ObjectStore spec differs from the live one:\n got %#v\nwant %#v", store["spec"], want)
	}
	if nestedMap(store, "metadata", "annotations")["helm.sh/resource-policy"] != "keep" {
		t.Error("ObjectStore must survive helm uninstall")
	}
	cluster := findRenderedDoc(t, docs, "Cluster", "astronomer-cnpg")
	plugins, _ := nestedMap(cluster, "spec")["plugins"].([]any)
	if len(plugins) != 1 || stringAt(plugins[0].(map[string]any), "parameters", "barmanObjectName") != "offhost-archive" {
		t.Fatalf("Cluster plugins do not reference the chart-managed store: %#v", plugins)
	}
}

func indent(text, prefix string) string {
	lines := strings.Split(strings.Trim(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestScheduledBackupImmediateAndShutdownKnobs(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplateLive(t, []string{liveValuesFile},
		"postgres.cloudNativePG.barmanCloud.scheduledBackup.immediate=true",
		"postgres.cloudNativePG.stopDelay=60",
		"postgres.cloudNativePG.smartShutdownTimeout=15",
	))
	backup := findRenderedDoc(t, docs, "ScheduledBackup", "astronomer-cnpg")
	if nestedMap(backup, "spec")["immediate"] != true {
		t.Errorf("ScheduledBackup.spec.immediate not rendered: %#v", backup["spec"])
	}
	spec := nestedMap(findRenderedDoc(t, docs, "Cluster", "astronomer-cnpg"), "spec")
	if fmt.Sprint(spec["stopDelay"]) != "60" || fmt.Sprint(spec["smartShutdownTimeout"]) != "15" {
		t.Errorf("shutdown knobs not rendered: stopDelay=%v smartShutdownTimeout=%v", spec["stopDelay"], spec["smartShutdownTimeout"])
	}
	// Defaults: CNPG defaults stay in force and no ObjectStore is rendered.
	docs = parseRenderedDocs(t, helmTemplateLive(t, []string{liveValuesFile}))
	spec = nestedMap(findRenderedDoc(t, docs, "Cluster", "astronomer-cnpg"), "spec")
	if _, ok := spec["stopDelay"]; ok {
		t.Error("stopDelay rendered by default")
	}
	for _, doc := range docs {
		if doc["kind"] == "ObjectStore" {
			t.Error("ObjectStore rendered without objectStore.create")
		}
	}
}

func TestChartManagedObjectStoreRequiresCredentialsRef(t *testing.T) {
	_, stderr, err := helmTemplateRaw(t, k3sKubeVersion, []string{liveValuesFile},
		"postgres.cloudNativePG.barmanCloud.objectStore.create=true",
		"postgres.cloudNativePG.barmanCloud.objectStore.destinationPath=s3://bucket/astronomer/",
	)
	if err == nil || !strings.Contains(stderr, "s3Credentials.accessKeyId.name is required") {
		t.Fatalf("missing credentials ref accepted: %v %s", err, stderr)
	}
	_, stderr, err = helmTemplateRaw(t, k3sKubeVersion, []string{liveValuesFile},
		"postgres.cloudNativePG.barmanCloud.objectStore.destinationPath=bucket-without-scheme",
	)
	if err == nil || !strings.Contains(stderr, "destinationPath") {
		t.Fatalf("non-s3 destinationPath accepted: %v %s", err, stderr)
	}
}
