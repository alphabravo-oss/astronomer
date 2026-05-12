// Package deploy — chart-render coverage for the management-plane log
// forwarder templates (FEATURES-051226 T03).
//
// These tests shell out to the system `helm` binary because the
// Fluent Bit pipeline is the kind of thing that needs to be a
// byte-for-byte match of a known-good config — a Go-side templating
// reimplementation would just be a worse Helm.
//
// All tests are auto-skipped when `helm` isn't on PATH so the same
// test file is happy on CI runners that ship without it.
package deploy

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// helmTemplate runs `helm template astronomer <chart> -f values.yaml --set ...`
// and returns the stdout. Fails the test on any non-zero exit.
func helmTemplate(t *testing.T, sets ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skipf("helm binary not on PATH (%v); skipping chart-render test", err)
	}

	// Resolve the chart path relative to this test file so the test
	// works no matter what the working directory is.
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	chartDir := filepath.Join(filepath.Dir(here), "chart")
	valuesFile := filepath.Join(chartDir, "values.yaml")

	args := []string{"template", "astronomer", chartDir, "-f", valuesFile}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	cmd := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template failed: %v\nstderr=%s", err, stderr.String())
	}
	return stdout.String()
}

func TestManagementLoggingConfigMap_RendersForLoki(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.endpoint=http://loki.observability.svc:3100",
		"managementLogging.auth.bearerSecretRef.name=loki-creds",
	)

	// Header: the ConfigMap exists and is named per the helper.
	if !strings.Contains(out, "name: astronomer-mgmt-logging-config") {
		t.Fatalf("ConfigMap name not rendered:\n%s", out)
	}
	// INPUT: server + worker paths only (default values; agent off).
	if !strings.Contains(out, "/var/log/containers/astronomer-server-*.log") {
		t.Fatalf("server tail path missing:\n%s", out)
	}
	if !strings.Contains(out, "/var/log/containers/astronomer-worker-*.log") {
		t.Fatalf("worker tail path missing:\n%s", out)
	}
	if strings.Contains(out, "/var/log/containers/astronomer-agent-*.log") {
		t.Fatalf("agent path rendered but includeAgent should default to false:\n%s", out)
	}
	// OUTPUT: loki stanza with the right host/port + bearer secret.
	if !strings.Contains(out, "Name              loki") {
		t.Fatalf("loki OUTPUT stanza missing:\n%s", out)
	}
	if !strings.Contains(out, "Host              loki.observability.svc") {
		t.Fatalf("loki host not split off endpoint:\n%s", out)
	}
	if !strings.Contains(out, "Port              3100") {
		t.Fatalf("loki port not split off endpoint:\n%s", out)
	}
	if !strings.Contains(out, "${BEARER_TOKEN}") {
		t.Fatalf("bearer-token substitution missing:\n%s", out)
	}
	// FILTER: kubernetes + record_modifier + grep (excludeDebug=true).
	if !strings.Contains(out, "Name                kubernetes") {
		t.Fatalf("kubernetes filter missing:\n%s", out)
	}
	if !strings.Contains(out, "Name          grep") || !strings.Contains(out, `Exclude       log /(?i)\bdebug\b/`) {
		t.Fatalf("excludeDebug grep filter missing:\n%s", out)
	}
}

func TestManagementLoggingConfigMap_RendersForElasticsearch(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.backend=elasticsearch",
		"managementLogging.endpoint=https://es.observability.svc:9200",
		"managementLogging.elasticsearch.index=astronomer",
	)
	if !strings.Contains(out, "Name              es") {
		t.Fatalf("es OUTPUT stanza missing:\n%s", out)
	}
	if !strings.Contains(out, "Host              es.observability.svc") {
		t.Fatalf("es host wrong:\n%s", out)
	}
	if !strings.Contains(out, "Port              9200") {
		t.Fatalf("es port wrong:\n%s", out)
	}
	// https:// flips Tls On automatically.
	if !strings.Contains(out, "Tls               On") {
		t.Fatalf("https endpoint should set Tls On:\n%s", out)
	}
	if !strings.Contains(out, "Index             astronomer") {
		t.Fatalf("ES Index missing:\n%s", out)
	}
	if !strings.Contains(out, "Logstash_Format   On") {
		t.Fatalf("Logstash_Format On missing (default true):\n%s", out)
	}
	// And the Loki stanza must NOT be present.
	if strings.Contains(out, "Name              loki") {
		t.Fatalf("backend=elasticsearch rendered loki OUTPUT:\n%s", out)
	}
}

func TestManagementLoggingConfigMap_RendersForSplunk(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.backend=splunk",
		"managementLogging.endpoint=https://splunk-hec.observability.svc:8088",
		"managementLogging.splunk.hecTokenSecretRef.name=splunk-creds",
	)
	if !strings.Contains(out, "Name              splunk") {
		t.Fatalf("splunk OUTPUT stanza missing:\n%s", out)
	}
	if !strings.Contains(out, "${SPLUNK_HEC_TOKEN}") {
		t.Fatalf("splunk HEC token env-var ref missing:\n%s", out)
	}
}

func TestManagementLoggingConfigMap_RendersForHTTP(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.backend=http",
		"managementLogging.endpoint=https://logs.example.com/ingest/v1",
	)
	if !strings.Contains(out, "Name              http") {
		t.Fatalf("http OUTPUT stanza missing:\n%s", out)
	}
	// URI must be the /path tail off the endpoint, with the host
	// portion split off into Host/Port.
	if !strings.Contains(out, "Host              logs.example.com") {
		t.Fatalf("http host wrong:\n%s", out)
	}
	if !strings.Contains(out, "URI               /ingest/v1") {
		t.Fatalf("http URI path not split off endpoint:\n%s", out)
	}
}

func TestManagementLoggingDaemonSet_RespectsImageRegistry(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.endpoint=http://loki:3100",
		"image.registry=internal.example.com",
	)
	// The T23 air-gapped registry override prepends to the third-party
	// image just like every other utility image in the chart.
	if !strings.Contains(out, "internal.example.com/fluent/fluent-bit:3.2.4") {
		t.Fatalf("image.registry override not applied to fluent-bit image:\n%s", out)
	}
	// And the DaemonSet itself must exist.
	if !strings.Contains(out, "kind: DaemonSet") || !strings.Contains(out, "name: astronomer-mgmt-logging") {
		t.Fatalf("DaemonSet missing:\n%s", out)
	}
	// Hash annotation rolls pods on values change.
	if !strings.Contains(out, "checksum/config:") {
		t.Fatalf("checksum/config annotation missing:\n%s", out)
	}
}

func TestManagementLogging_DisabledRendersNothing(t *testing.T) {
	out := helmTemplate(t)
	for _, want := range []string{
		"mgmt-logging",
		"management-logging",
	} {
		if strings.Contains(out, want) {
			t.Fatalf("default (disabled) render contains %q — expected nothing:\n%s", want, out)
		}
	}
}

func TestManagementLogging_RBACGrantsExist(t *testing.T) {
	out := helmTemplate(t,
		"managementLogging.enabled=true",
		"managementLogging.endpoint=http://loki:3100",
	)
	// SA + ClusterRole + ClusterRoleBinding all present.
	if !strings.Contains(out, "kind: ServiceAccount") {
		t.Fatalf("ServiceAccount missing:\n%s", out)
	}
	if !strings.Contains(out, "kind: ClusterRole") || !strings.Contains(out, "kind: ClusterRoleBinding") {
		t.Fatalf("ClusterRole/Binding missing:\n%s", out)
	}
	// Verify the role grants pods + namespaces but NOT secrets.
	if !strings.Contains(out, "- pods") || !strings.Contains(out, "- namespaces") {
		t.Fatalf("RBAC missing pod/namespace verbs:\n%s", out)
	}
	// Find the management-logging ClusterRole rules section and make
	// sure it doesn't accidentally include secrets.
	// (The rest of the chart's ClusterRoles may include secrets — we only
	// care that the mgmt-logging ClusterRole doesn't.)
	idx := strings.Index(out, "name: astronomer-mgmt-logging\n  labels:")
	if idx == -1 {
		t.Fatalf("astronomer-mgmt-logging ClusterRole header not found:\n%s", out)
	}
	// Take the slice from that header until the next `---` document
	// separator; the role's `rules:` block lives inside this window.
	tail := out[idx:]
	next := strings.Index(tail, "\n---\n")
	window := tail
	if next != -1 {
		window = tail[:next]
	}
	if strings.Contains(window, "- secrets") {
		t.Fatalf("management-logging ClusterRole accidentally grants secrets read:\n%s", window)
	}
}
