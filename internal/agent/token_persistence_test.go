package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const (
	testBootstrapToken = "bootstrap-token-material-0000000001"
	testLegacyToken    = "legacy-token-material-00000000000001"
	testIdentityToken  = "identity-token-material-000000000001"
	testRotatedToken   = "rotated-token-material-000000000001"
)

func credentialTestConfig(environmentFallback string) *AgentConfig {
	return &AgentConfig{
		AgentToken:               environmentFallback,
		BootstrapTokenSecretName: "astronomer-agent-registration-token",
		BootstrapTokenSecretKey:  "token",
		IdentityTokenSecretName:  "astronomer-agent-identity",
		IdentityTokenSecretKey:   "token",
		LegacyTokenSecretName:    "astronomer-agent-token",
		LegacyTokenSecretKey:     "token",
	}
}

func testCredentialSecret(name, token string) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "astronomer-system"}}
	if token != "" {
		secret.Data = map[string][]byte{"token": []byte(token)}
	}
	return secret
}

func emptyIdentityContainer() *corev1.Secret {
	secret := testCredentialSecret("astronomer-agent-identity", "")
	secret.Labels = map[string]string{credentialIdentityLabel: credentialIdentityPurpose}
	return secret
}

func TestCredentialResolutionOrderAndBootstrapAPIRead(t *testing.T) {
	ctx := context.Background()
	cfg := credentialTestConfig("environment-must-not-be-used-0001")
	bootstrap := testCredentialSecret(cfg.BootstrapTokenSecretName, testBootstrapToken)
	legacy := testCredentialSecret(cfg.LegacyTokenSecretName, testLegacyToken)
	identity := emptyIdentityContainer()

	client := fake.NewSimpleClientset(identity, legacy, bootstrap)
	got, source, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil || got != testLegacyToken || source != credentialSourceLegacy {
		t.Fatalf("legacy migration resolution = (%q, %q, %v)", got, source, err)
	}
	if err := client.CoreV1().Secrets("astronomer-system").Delete(ctx, cfg.LegacyTokenSecretName, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	got, source, err = resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil || got != testBootstrapToken || source != credentialSourceBootstrap {
		t.Fatalf("bootstrap API resolution = (%q, %q, %v)", got, source, err)
	}

	identity.Data = map[string][]byte{"token": []byte(testIdentityToken)}
	if _, err := client.CoreV1().Secrets("astronomer-system").Update(ctx, identity, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	got, source, err = resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil || got != testIdentityToken || source != CredentialSourceIdentity {
		t.Fatalf("active identity resolution = (%q, %q, %v)", got, source, err)
	}
}

func TestIdentityContainerAndReadErrorsFailClosed(t *testing.T) {
	ctx := context.Background()
	cfg := credentialTestConfig(testBootstrapToken)
	for _, tt := range []struct {
		name     string
		identity *corev1.Secret
	}{
		{name: "missing container", identity: nil},
		{name: "empty missing purpose", identity: testCredentialSecret(cfg.IdentityTokenSecretName, "")},
		{name: "nonempty invalid", identity: testCredentialSecret(cfg.IdentityTokenSecretName, "bad")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{testCredentialSecret(cfg.BootstrapTokenSecretName, testBootstrapToken)}
			if tt.identity != nil {
				objects = append(objects, tt.identity)
			}
			client := fake.NewSimpleClientset(objects...)
			if _, _, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg); err == nil {
				t.Fatal("invalid identity container state must fail closed")
			}
		})
	}

	for _, failedName := range []string{cfg.IdentityTokenSecretName, cfg.LegacyTokenSecretName, cfg.BootstrapTokenSecretName} {
		t.Run("read error "+failedName, func(t *testing.T) {
			client := fake.NewSimpleClientset(emptyIdentityContainer(), testCredentialSecret(cfg.BootstrapTokenSecretName, testBootstrapToken))
			client.PrependReactor("get", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
				if action.(ktesting.GetAction).GetName() == failedName {
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, failedName, errors.New("denied"))
				}
				return false, nil, nil
			})
			if _, _, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg); err == nil {
				t.Fatal("non-NotFound read error must fail closed")
			}
		})
	}
}

func TestDurablePersistenceUsesExactSSAAndScrubsLegacyAnnotations(t *testing.T) {
	ctx := context.Background()
	cfg := credentialTestConfig("")
	identity := emptyIdentityContainer()
	identity.Annotations = map[string]string{lastAppliedAnnotation: "sensitive-legacy-document"}
	legacy := testCredentialSecret(cfg.LegacyTokenSecretName, testLegacyToken)
	legacy.Annotations = map[string]string{lastAppliedAnnotation: "other-sensitive-document"}
	client := fake.NewSimpleClientset(identity, legacy)

	var applyPayload []byte
	var scrubPatchCount int
	client.PrependReactor("patch", "secrets", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		withOptions, ok := action.(interface{ GetPatchOptions() metav1.PatchOptions })
		if !ok || withOptions.GetPatchOptions().FieldManager != credentialFieldManager {
			t.Fatal("credential patch missing dedicated field manager")
		}
		switch patch.GetPatchType() {
		case types.MergePatchType:
			scrubPatchCount++
			if strings.Contains(string(patch.GetPatch()), "sensitive") {
				t.Fatal("annotation scrub copied annotation content")
			}
			return true, testCredentialSecret(patch.GetName(), testLegacyToken), nil
		case types.ApplyPatchType:
			if force := withOptions.GetPatchOptions().Force; force == nil || !*force {
				t.Fatal("authoritative data.token apply must deliberately force ownership")
			}
			if patch.GetName() != cfg.IdentityTokenSecretName {
				t.Fatalf("SSA patched %q, want active identity", patch.GetName())
			}
			applyPayload = append([]byte(nil), patch.GetPatch()...)
			return true, testCredentialSecret(cfg.IdentityTokenSecretName, testRotatedToken), nil
		default:
			t.Fatalf("unexpected patch type %q", patch.GetPatchType())
			return true, nil, nil
		}
	})
	if err := persistRotatedTokenWithClient(ctx, client, "astronomer-system", cfg, testRotatedToken); err != nil {
		t.Fatal(err)
	}
	// Repeat against the same observed annotations. Merge-null remains safe if
	// another actor already removed them between GET and PATCH.
	if err := persistRotatedTokenWithClient(ctx, client, "astronomer-system", cfg, testRotatedToken); err != nil {
		t.Fatalf("idempotent persistence retry: %v", err)
	}
	if scrubPatchCount != 4 {
		t.Fatalf("annotation scrub patches = %d, want two idempotent identity + legacy pairs", scrubPatchCount)
	}
	var applied map[string]any
	if err := json.Unmarshal(applyPayload, &applied); err != nil {
		t.Fatal(err)
	}
	metadata := applied["metadata"].(map[string]any)
	if _, ok := metadata["annotations"]; ok {
		t.Fatal("SSA payload unexpectedly owns annotations")
	}
	if _, ok := metadata["labels"]; ok {
		t.Fatal("SSA payload unexpectedly owns installer labels")
	}
	if _, ok := applied["data"]; !ok {
		t.Fatal("SSA payload does not own data.token")
	}
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" || action.GetVerb() == "update" {
			t.Fatalf("persistence used forbidden %s", action.GetVerb())
		}
	}
}

func TestWrongClusterIdentityNeverFallsBack(t *testing.T) {
	identity := testCredentialSecret("astronomer-agent-identity", "wrong-cluster-durable-token-00000001")
	client := fake.NewSimpleClientset(identity, testCredentialSecret("astronomer-agent-registration-token", testBootstrapToken))
	got, source, err := resolveCredentialFromSecrets(context.Background(), client, "astronomer-system", credentialTestConfig(testBootstrapToken))
	if err != nil {
		t.Fatal(err)
	}
	if got != "wrong-cluster-durable-token-00000001" || source != CredentialSourceIdentity {
		t.Fatalf("selected (%q, %q), want identity and no fallback", got, source)
	}
}
