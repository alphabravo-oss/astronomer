// Package deploy — chart-render coverage for the unconditional key-material
// guard (dev-keys-default-and-silent).
//
// The chart used to ship a working JWT signing key and encryption key as its
// defaults and only rejected them under config.env=production, so
// `helm install astronomer ./deploy/chart` produced a management plane whose
// tokens anyone holding this repository could forge. The guard now runs on
// every render, so these tests are the contract that a keyless install is
// impossible — including in development.
package deploy

import (
	"strings"
	"testing"
)

const (
	devSentinelSecretKey     = "local-dev-secret-key-change-in-production"
	devSentinelEncryptionKey = "AGE-SECRET-KEY-1G68X62WMPA92CASLAK8WSN0V6HGTHNQ4Z50XGQXKDMS55SGJZG5SARN6N7"
)

func TestChartRefusesToRenderWithoutKeyMaterial(t *testing.T) {
	errOut := helmTemplateExpectError(t, nil)
	for _, want := range []string{
		"secrets.secretKey is empty",
		"secrets.encryptionKey is empty",
		// The message has to carry the generation recipe, or the operator's
		// next move is to go looking for a default to copy.
		"openssl rand -base64 32",
		"astro admin generate-encryption-key",
		"secrets.existingSecret",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("keyless render error missing %q:\n%s", want, errOut)
		}
	}
}

func TestChartRefusesTheHistoricalDevSentinels(t *testing.T) {
	for _, tc := range []struct {
		name string
		sets []string
		want string
	}{
		{
			name: "signing key",
			sets: []string{"secrets.secretKey=" + devSentinelSecretKey, testRenderEncryptionKeySet},
			want: "secrets.secretKey is the chart's published development value",
		},
		{
			name: "encryption key",
			sets: []string{testRenderSecretKeySet, "secrets.encryptionKey=" + devSentinelEncryptionKey},
			want: "secrets.encryptionKey contains the chart's published development age key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// config.env=development is the point: the old guard only fired
			// for production.
			errOut := helmTemplateExpectError(t, nil, append(tc.sets, "config.env=development")...)
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("sentinel render error missing %q:\n%s", tc.want, errOut)
			}
		})
	}
}

func TestChartRendersWithOperatorSuppliedKeys(t *testing.T) {
	out := helmTemplate(t)
	if !strings.Contains(out, "chart-render-test-jwt-signing-key") {
		t.Fatalf("supplied signing key not rendered into the Secret:\n%s", out)
	}
	for _, sentinel := range []string{devSentinelSecretKey, devSentinelEncryptionKey} {
		if strings.Contains(out, sentinel) {
			t.Fatalf("render still carries the published dev value %q", sentinel)
		}
	}
}

func TestChartRendersKeylessWithExistingSecret(t *testing.T) {
	// The external-secret-manager posture: no inline key material anywhere,
	// and the guard must not stand in its way.
	out := helmTemplateWithValueFiles(t, nil,
		"secrets.existingSecret=core-credentials",
		"secrets.secretKey=",
		"secrets.encryptionKey=",
		"postgres.bundled.enabled=false",
		"postgres.external.dsnSecretRef.name=postgres-credentials",
		"postgres.external.dsnSecretRef.key=dsn",
	)
	if !strings.Contains(out, "core-credentials") {
		t.Fatalf("existingSecret render did not reference the pre-created Secret:\n%s", out)
	}
}

func TestChartRejectsExistingSecretWithBundledPostgres(t *testing.T) {
	errOut := helmTemplateExpectError(t, nil,
		"secrets.existingSecret=core-credentials",
		"secrets.secretKey=",
		"secrets.encryptionKey=",
	)
	if !strings.Contains(errOut, "secrets.existingSecret cannot be combined with postgres.bundled.enabled=true") {
		t.Fatalf("unsafe bundled database Secret posture was not rejected:\n%s", errOut)
	}
}

// The published dev identity is caught as a previous keyring entry too, and a
// Fernet key (or any non-age entry) is refused outright.
func TestChartRejectsNonAgeOrDevKeyringEntries(t *testing.T) {
	for _, tc := range []struct {
		name, key, want string
	}{
		{"dev key as previous entry", "new:AGE-SECRET-KEY-19RJN0RE0F29YSVD40EJR7JQHT388FHUSF2R8RLXGMVZW2KHQZHEQ6WPG8U\\,old:" + devSentinelEncryptionKey, "published development age key"},
		{"fernet key", "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA=", "not an age identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errOut := helmTemplateExpectError(t, nil, testRenderSecretKeySet, "secrets.encryptionKey="+tc.key, "config.env=development")
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("render error missing %q:\n%s", tc.want, errOut)
			}
		})
	}
}
