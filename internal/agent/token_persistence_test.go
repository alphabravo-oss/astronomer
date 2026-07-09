package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

const (
	testBootstrapToken = "bootstrap-token-material-0000000001"
	testDurableToken   = "durable-token-material-000000000001"
	testRotatedToken   = "rotated-token-material-000000000001"
)

func credentialTestConfig(bootstrap string) *AgentConfig {
	return &AgentConfig{
		AgentToken:               bootstrap,
		BootstrapTokenSecretName: "astronomer-agent-registration-token",
		BootstrapTokenSecretKey:  "token",
		DurableTokenSecretName:   "astronomer-agent-token",
		DurableTokenSecretKey:    "token",
	}
}

func durableSecret(token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "astronomer-agent-token", Namespace: "astronomer-system"},
		Data:       map[string][]byte{"token": []byte(token)},
	}
}

func TestFreshAdoptionWritesDurableAndReapplyCannotDisplaceIt(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	cfg := credentialTestConfig(testBootstrapToken)

	token, source, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil {
		t.Fatalf("resolve fresh bootstrap: %v", err)
	}
	if token != testBootstrapToken || source != credentialSourceBootstrap {
		t.Fatalf("fresh credential = (%q, %q), want bootstrap", token, source)
	}
	if err := persistRotatedTokenWithClient(ctx, client, "astronomer-system", cfg, testDurableToken); err != nil {
		t.Fatalf("persist adoption token: %v", err)
	}

	// Simulate any installer reapply, including one after the original
	// registration token has expired and a fresh bootstrap was minted. The
	// durable Secret remains a distinct object and must always win.
	for _, reappliedBootstrap := range []string{testBootstrapToken, "reminted-bootstrap-material-00000001"} {
		cfg.AgentToken = reappliedBootstrap
		got, gotSource, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
		if err != nil {
			t.Fatalf("resolve after bootstrap reapply: %v", err)
		}
		if got != testDurableToken || gotSource != credentialSourceDurable {
			t.Fatalf("credential after reapply = (%q, %q), want durable", got, gotSource)
		}
	}
}

func TestRestartAndRotationUseOnlyDurableSecret(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(durableSecret(testDurableToken))
	cfg := credentialTestConfig(testBootstrapToken)
	if err := persistRotatedTokenWithClient(ctx, client, "astronomer-system", cfg, testRotatedToken); err != nil {
		t.Fatalf("persist rotation: %v", err)
	}
	got, source, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil {
		t.Fatalf("resolve rotated token after restart: %v", err)
	}
	if got != testRotatedToken || source != credentialSourceDurable {
		t.Fatalf("restart credential = (%q, %q), want rotated durable", got, source)
	}
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "secrets" || action.GetVerb() == "list" {
			continue
		}
		if named, ok := action.(ktesting.GetAction); ok && named.GetName() != cfg.DurableTokenSecretName {
			t.Fatalf("read unexpected Secret %q", named.GetName())
		}
		if named, ok := action.(ktesting.UpdateAction); ok {
			secret := named.GetObject().(*corev1.Secret)
			if secret.Name != cfg.DurableTokenSecretName {
				t.Fatalf("updated unexpected Secret %q", secret.Name)
			}
		}
	}
}

func TestDeletedDurableFallsBackOnlyToValidBootstrap(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset(durableSecret(testDurableToken))
	if err := client.CoreV1().Secrets("astronomer-system").Delete(ctx, "astronomer-agent-token", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := credentialTestConfig(testBootstrapToken)
	got, source, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg)
	if err != nil || got != testBootstrapToken || source != credentialSourceBootstrap {
		t.Fatalf("valid bootstrap fallback = (%q, %q, %v)", got, source, err)
	}
	cfg.AgentToken = "bad"
	if _, _, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", cfg); err == nil {
		t.Fatal("malformed bootstrap must fail closed")
	}
}

func TestDurableReadErrorsAndInvalidMaterialFailClosed(t *testing.T) {
	ctx := context.Background()
	marker := testBootstrapToken
	client := fake.NewSimpleClientset()
	client.PrependReactor("get", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "astronomer-agent-token", errors.New("denied"))
	})
	if _, _, err := resolveCredentialFromSecrets(ctx, client, "astronomer-system", credentialTestConfig(marker)); err == nil {
		t.Fatal("Forbidden durable read must not fall back to bootstrap")
	} else if strings.Contains(err.Error(), marker) {
		t.Fatal("credential material leaked in diagnostic")
	}

	invalid := fake.NewSimpleClientset(durableSecret("bad"))
	if _, _, err := resolveCredentialFromSecrets(ctx, invalid, "astronomer-system", credentialTestConfig(marker)); err == nil {
		t.Fatal("invalid durable material must not fall back to bootstrap")
	}
}

func TestWrongClusterDurableIdentityNeverFallsBackToBootstrap(t *testing.T) {
	// Opaque credentials cannot be cluster-validated locally; the tunnel server
	// performs that binding check. The startup invariant is that a shape-valid
	// durable token is selected exclusively, so a wrong-cluster rejection cannot
	// trigger bootstrap downgrade.
	client := fake.NewSimpleClientset(durableSecret("wrong-cluster-durable-token-00000001"))
	got, source, err := resolveCredentialFromSecrets(context.Background(), client, "astronomer-system", credentialTestConfig(testBootstrapToken))
	if err != nil {
		t.Fatal(err)
	}
	if got != "wrong-cluster-durable-token-00000001" || source != credentialSourceDurable {
		t.Fatalf("selected (%q, %q), want durable and no fallback", got, source)
	}
}
