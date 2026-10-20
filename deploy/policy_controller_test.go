package deploy

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgradecli/toolchain"
)

func TestChartTrustedRootEqualsEmbeddedToolchainRoot(t *testing.T) {
	chartCopy, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "chart", "files", "sigstore", "trusted_root.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(chartCopy, toolchain.TrustedRootBytes()) {
		t.Fatal("deploy/chart/files/sigstore/trusted_root.json differs from internal/ops/upgradecli/toolchain/trust/trusted_root.json; copy the toolchain root into the chart")
	}
}

func deliveryTrustVolume(t *testing.T, docs []renderedDoc, deployment string) string {
	t.Helper()
	volumes, _ := podSpecFor(findRenderedDoc(t, docs, "Deployment", deployment))["volumes"].([]any)
	for _, volume := range volumes {
		v := volume.(map[string]any)
		if v["name"] == "delivery-source-trust" {
			return stringAt(v, "secret", "secretName")
		}
	}
	return ""
}

func TestChartManagedDeliveryTrustedRootIsTheDefault(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	secret := findRenderedDoc(t, docs, "Secret", "astronomer-delivery-trust")
	raw, err := base64.StdEncoding.DecodeString(stringAt(secret, "data", "trusted_root.json"))
	if err != nil || !bytes.Equal(raw, toolchain.TrustedRootBytes()) {
		t.Fatalf("chart-managed trusted_root.json does not carry the pinned root (err=%v)", err)
	}
	for _, deployment := range []string{"astronomer-server", "astronomer-worker"} {
		if got := deliveryTrustVolume(t, docs, deployment); got != "astronomer-delivery-trust" {
			t.Errorf("%s mounts delivery trust %q, want the chart-managed Secret", deployment, got)
		}
	}

	docs = parseRenderedDocs(t, helmTemplate(t, "delivery.sourceResolution.trustKeys.existingSecret=site-trust"))
	for _, doc := range docs {
		if doc["kind"] == "Secret" && stringAt(doc, "metadata", "name") == "astronomer-delivery-trust" {
			t.Error("chart-managed trust Secret rendered although existingSecret is set")
		}
	}
	if got := deliveryTrustVolume(t, docs, "astronomer-server"); got != "site-trust" {
		t.Errorf("server mounts %q, want the operator Secret", got)
	}

	docs = parseRenderedDocs(t, helmTemplate(t, "delivery.sourceResolution.trustKeys.chartManagedTrustedRoot=false"))
	if got := deliveryTrustVolume(t, docs, "astronomer-server"); got != "" {
		t.Errorf("opt-out still mounts %q", got)
	}
}

type clusterImagePolicy struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Mode   string `json:"mode"`
		Images []struct {
			Glob string `json:"glob"`
		} `json:"images"`
		Authorities []struct {
			Name    string         `json:"name"`
			Keyless map[string]any `json:"keyless"`
			Key     map[string]any `json:"key"`
			Static  struct {
				Action string `json:"action"`
			} `json:"static"`
		} `json:"authorities"`
	} `json:"spec"`
}

func loadClusterImagePolicies(t *testing.T) []clusterImagePolicy {
	t.Helper()
	raw := readRepoFile(t, filepath.Join("deploy", "policy-controller", "cip-astronomer.yaml"))
	var policies []clusterImagePolicy
	for _, part := range strings.Split(raw, "\n---\n") {
		var policy clusterImagePolicy
		if err := yaml.Unmarshal([]byte(part), &policy); err != nil {
			t.Fatal(err)
		}
		if policy.Metadata.Name != "" {
			policies = append(policies, policy)
		}
	}
	return policies
}

// policyGlobMatch mirrors policy-controller's glob semantics: "**" matches any
// run of characters, "*" any run without "/", everything else literally.
func policyGlobMatch(glob, ref string) bool {
	var expr strings.Builder
	expr.WriteString("^")
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**"):
			expr.WriteString(".*")
			i++
		case glob[i] == '*':
			expr.WriteString("[^/]*")
		default:
			expr.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	expr.WriteString("$")
	return regexp.MustCompile(expr.String()).MatchString(ref)
}

// canonicalImage applies go-containerregistry's defaulting: no registry means
// index.docker.io, a single path element means library/.
func canonicalImage(ref string) string {
	first, rest, found := strings.Cut(ref, "/")
	if !found || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		if !found {
			return "index.docker.io/library/" + ref
		}
		return "index.docker.io/" + ref
	}
	if first == "docker.io" {
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
		return "index.docker.io/" + rest
	}
	return ref
}

// operatorInjectedImages run in the Astronomer namespace without being set by
// the chart: the CNPG bootstrap/plugin sidecar and cert-manager's HTTP-01 solver.
var operatorInjectedImages = []string{
	"ghcr.io/cloudnative-pg/cloudnative-pg:1.30.1@sha256:923c267ec29636db3bee20f993d0ec4973fa22998e1adad37da79e4d32b5bc07",
	"ghcr.io/cloudnative-pg/plugin-barman-cloud-sidecar:v0.15.1@sha256:2287a793a804db751edd0d07d44cd549e357677e98a800eb12e2cf7c0d53f9b0",
	"quay.io/jetstack/cert-manager-acmesolver:v1.20.2",
}

func renderedImages(docs []renderedDoc) []string {
	var images []string
	for _, doc := range docs {
		if doc["kind"] == "Cluster" {
			images = append(images, stringAt(doc, "spec", "imageName"))
		}
		podSpec := podSpecFor(doc)
		if podSpec == nil {
			continue
		}
		for _, field := range []string{"initContainers", "containers"} {
			for _, container := range containerList(podSpec, field) {
				images = append(images, stringValue(container["image"]))
			}
		}
	}
	return images
}

func TestEveryChartImageIsCoveredBySignaturePolicy(t *testing.T) {
	policies := loadClusterImagePolicies(t)
	renders := map[string]string{
		"live": helmTemplateLive(t, []string{liveValuesFile, liveOverlayFile}),
		"dev-all-optional": helmTemplate(t,
			"postgres.mode=cloudNativePG",
			"postgres.cloudNativePG.barmanCloud.enabled=true",
			"postgres.cloudNativePG.barmanCloud.objectStoreName=store",
			"dex.enabled=true",
			"managementLogging.enabled=true",
			"managementLogging.endpoint=http://loki.observability.svc:3100",
			"managementLogging.auth.bearerSecretRef.name=loki-creds",
			"managementBackup.s3.bucket=b",
			"managementBackup.s3.credentialsSecretRef.name=c",
			"managementBackup.encryption.sourceIdentity=i",
			"managementBackup.encryption.wrappingSecretRef.name=w",
			"managementRestoreDrill.enabled=true",
		),
		"dev-bundled": helmTemplate(t),
	}
	seen := map[string]bool{}
	for profile, out := range renders {
		images := append(renderedImages(parseRenderedDocs(t, out)), operatorInjectedImages...)
		for _, image := range images {
			if image == "" || seen[image] {
				continue
			}
			seen[image] = true
			ref := canonicalImage(image)
			var matched []string
			for _, policy := range policies {
				for _, glob := range policy.Spec.Images {
					if policyGlobMatch(glob.Glob, ref) {
						matched = append(matched, policy.Metadata.Name)
					}
				}
			}
			if len(matched) == 0 {
				t.Errorf("%s: image %s (%s) matches no ClusterImagePolicy; no-match-policy=deny would reject it", profile, image, ref)
			}
		}
	}
	for _, want := range []string{"index.docker.io/library/busybox", "ghcr.io/dexidp/dex", "index.docker.io/fluent/fluent-bit", "ghcr.io/cloudnative-pg/postgresql"} {
		found := false
		for image := range seen {
			found = found || strings.HasPrefix(canonicalImage(image), want)
		}
		if !found {
			t.Errorf("coverage renders never produced %s; the test lost a profile", want)
		}
	}
}

func TestSignaturePoliciesUseVendorAuthorities(t *testing.T) {
	values := readRepoFile(t, filepath.Join("deploy", "policy-controller", "values.yaml"))
	if !regexp.MustCompile(`(?m)^\s+no-match-policy: deny$`).MatchString(values) || !strings.Contains(values, "failurePolicy: Fail") {
		t.Fatal("policy-controller values must pin no-match-policy: deny and failurePolicy: Fail")
	}
	signed := map[string]string{
		"ghcr.io/alphabravo-oss/astronomer-go-server":        "keyless",
		"ghcr.io/cloudnative-pg/cloudnative-pg":              "static",
		"ghcr.io/cloudnative-pg/postgresql":                  "static",
		"ghcr.io/dexidp/dex":                                 "static",
		"index.docker.io/fluent/fluent-bit":                  "static",
		"quay.io/jetstack/cert-manager-acmesolver":           "key",
		"index.docker.io/library/busybox":                    "static",
		"ghcr.io/cloudnative-pg/plugin-barman-cloud-sidecar": "static",
	}
	for _, policy := range loadClusterImagePolicies(t) {
		if policy.Spec.Mode != "enforce" {
			t.Errorf("%s mode = %q", policy.Metadata.Name, policy.Spec.Mode)
		}
		for _, glob := range policy.Spec.Images {
			if strings.HasSuffix(glob.Glob, "/**") && policy.Spec.Authorities[0].Static.Action == "pass" {
				t.Errorf("%s passes a whole registry namespace %q", policy.Metadata.Name, glob.Glob)
			}
		}
		for repo, kind := range signed {
			for _, glob := range policy.Spec.Images {
				if !policyGlobMatch(glob.Glob, repo+":tag") {
					continue
				}
				authority := policy.Spec.Authorities[0]
				got := "static"
				switch {
				case authority.Keyless != nil:
					got = "keyless"
				case authority.Key != nil:
					got = "key"
				}
				if got != kind {
					t.Errorf("%s is covered by %s with a %s authority, want %s", repo, policy.Metadata.Name, got, kind)
				}
			}
		}
	}
}

func TestCNPGImageNamesAreTagAtDigest(t *testing.T) {
	values := readRepoFile(t, filepath.Join("deploy", "chart", "values.yaml"))
	m := regexp.MustCompile(`(?m)^    imageName: (\S+)$`).FindStringSubmatch(values)
	if m == nil || !regexp.MustCompile(`^ghcr\.io/cloudnative-pg/postgresql:[^@]+@sha256:[a-f0-9]{64}$`).MatchString(m[1]) {
		t.Fatalf("postgres.cloudNativePG.imageName must be tag@digest (policy-controller mutates bare tags and CNPG then loops): %v", m)
	}
}

func TestLiveOverlaySwitchesToChartManagedTrustSecret(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplateLive(t, []string{liveValuesFile, liveOverlayFile}))
	findRenderedDoc(t, docs, "Secret", "astronomer-delivery-trust")
	for _, deployment := range []string{"astronomer-server", "astronomer-worker"} {
		if got := deliveryTrustVolume(t, docs, deployment); got != "astronomer-delivery-trust" {
			t.Errorf("%s mounts %q", deployment, got)
		}
	}
	// Without the overlay the live values keep mounting the hand-made Secret.
	docs = parseRenderedDocs(t, helmTemplateLive(t, []string{liveValuesFile}))
	for _, doc := range docs {
		if doc["kind"] == "Secret" && stringAt(doc, "metadata", "name") == "astronomer-delivery-trust" {
			t.Error("live values (existingSecret set) must not render the chart-managed Secret")
		}
	}
	if got := deliveryTrustVolume(t, docs, "astronomer-server"); got != "astronomer-delivery-trust" {
		t.Errorf("live server mounts %q", got)
	}
}
