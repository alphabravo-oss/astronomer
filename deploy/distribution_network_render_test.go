package deploy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistributionMirrorPolicyAndTrustReachActualServerAndWorker(t *testing.T) {
	directory := t.TempDir()
	manifest := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"schema_version":1,"release":{"version":"v2.0.1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(directory, "values.yaml")
	writeValues := func(signature string) {
		data := "catalog:\n  enabled: false\ndelivery:\n  sourceResolution:\n    trustKeys:\n      existingSecret: approved-public-trust\n  distribution:\n    destinationPolicy:\n      restricted: true\n      origins:\n        - url: https://registry.internal.example\n          cidrs: [10.42.1.0/24]\n    mirrorEndorsement:\n      plan: '{\"schema_version\":1}'\n      signature: " + signature + "\n      keyRef: operator\n"
		if err := os.WriteFile(values, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeValues("first-detached-signature")
	for _, test := range []struct {
		sets     []string
		expected string
	}{
		{[]string{"delivery.distribution.mirrorEndorsement.signature="}, "distribution mirror endorsement requires"},
		{[]string{"catalog.enabled=true"}, "disconnected application catalog must"},
		{[]string{"delivery.distribution.unrecognized=true"}, "unrecognized"},
	} {
		sets := append([]string{testRenderSecretKeySet, testRenderEncryptionKeySet}, test.sets...)
		err := helmTemplateExpectErrorWithFlags(t, []string{values}, []string{"--set-file", "release.manifest=" + manifest}, sets...)
		if !strings.Contains(err, test.expected) {
			t.Fatalf("unexpected rejected mirror configuration: %s", err)
		}
	}
	first := helmTemplateWithValueFilesAndFlags(t, []string{values}, []string{"--set-file", "release.manifest=" + manifest})
	for _, required := range []string{"DISTRIBUTION_DESTINATION_POLICY:", "name: DISTRIBUTION_MIRROR_PLAN_PATH", "distribution-mirror.json:", "immutable: true", "secretName: \"approved-public-trust\"", "path: distribution-mirror.sig"} {
		if !strings.Contains(first, required) {
			t.Errorf("runtime mirror contract absent: %s", required)
		}
	}
	docs := parseRenderedDocs(t, first)
	for _, component := range []string{"server", "worker"} {
		deployment := findRenderedDoc(t, docs, "Deployment", "astronomer-"+component)
		containers := containerList(podSpecFor(deployment), "containers")
		for _, container := range containers {
			encoded := fmt.Sprint(container["envFrom"])
			if !strings.Contains(encoded, "astronomer-config") {
				t.Fatalf("%s does not consume destination policy ConfigMap", component)
			}
		}
	}
	name := func(render string) string {
		for _, line := range strings.Split(render, "\n") {
			if strings.Contains(line, "name: astronomer-release-manifest-") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	writeValues("second-detached-signature")
	second := helmTemplateWithValueFilesAndFlags(t, []string{values}, []string{"--set-file", "release.manifest=" + manifest})
	if name(first) == "" || name(first) == name(second) {
		t.Fatal("changed endorsement would patch an immutable ConfigMap")
	}
}
