package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	credentialReadTimeout       = 10 * time.Second
	credentialIdentityLabel     = "astronomer.io/agent-credential-purpose"
	credentialIdentityPurpose   = "durable-identity-container"
	credentialFieldManager      = "astronomer-agent-identity"
	CredentialSourceIdentity    = "durable_identity"
	credentialSourceLegacy      = "legacy_durable_secret"
	credentialSourceBootstrap   = "bootstrap_secret"
	CredentialSourceEnvironment = "environment"
	lastAppliedAnnotation       = "kubectl.kubernetes.io/last-applied-configuration"
)

var credentialWriteTimeout = 5 * time.Second

// resolveStartupCredential uses environment material only for the explicit
// off-cluster compatibility path. A Kubernetes-hosted agent resolves all three
// exact Secret names through the API so configured bootstrap/legacy names are
// live inputs and every non-NotFound read error can fail closed.
func resolveStartupCredential(ctx context.Context, cfg *AgentConfig, log *slog.Logger) error {
	if cfg == nil {
		return fmt.Errorf("agent config is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) == "" {
		cfg.AgentToken = strings.TrimSpace(cfg.AgentToken)
		cfg.CredentialSource = CredentialSourceEnvironment
		return nil
	}
	namespace, err := serviceAccountNamespace()
	if err != nil {
		return fmt.Errorf("resolve agent credential namespace: %w", err)
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("initialize in-cluster credential reader: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("initialize Kubernetes credential client: %w", err)
	}
	token, source, err := resolveCredentialFromSecrets(ctx, clientset, namespace, cfg)
	if err != nil {
		return err
	}
	cfg.AgentToken = token
	cfg.CredentialSource = source
	log.Debug("resolved agent credential", "credential_source", source)
	return nil
}

func resolveCredentialFromSecrets(ctx context.Context, client kubernetes.Interface, namespace string, cfg *AgentConfig) (string, string, error) {
	if client == nil || cfg == nil {
		return "", "", fmt.Errorf("Kubernetes credential client and agent config are required")
	}
	secrets := client.CoreV1().Secrets(namespace)
	identity, err := secrets.Get(ctx, cfg.IdentityTokenSecretName, metav1.GetOptions{})
	if err != nil {
		if cfg.LegacyLayoutConfigured && (apierrors.IsNotFound(err) || apierrors.IsForbidden(err)) {
			return resolveLegacyCredential(ctx, secrets, cfg)
		}
		if apierrors.IsNotFound(err) {
			return "", "", fmt.Errorf("active identity container is missing; apply the current registration manifest before starting this agent")
		}
		return "", "", fmt.Errorf("read active agent identity: %w", err)
	}
	identityToken := string(identity.Data[cfg.IdentityTokenSecretKey])
	if identityToken != "" {
		if err := validateCredentialShape(identityToken); err != nil {
			return "", "", fmt.Errorf("active agent identity has invalid token material: %w", err)
		}
		return identityToken, CredentialSourceIdentity, nil
	}
	if identity.Labels[credentialIdentityLabel] != credentialIdentityPurpose {
		return "", "", fmt.Errorf("empty active agent identity is missing its expected container-purpose label")
	}
	if cfg.LegacyLayoutConfigured {
		return "", "", fmt.Errorf("active identity is empty while the pod still uses the legacy credential layout; apply the current registration manifest")
	}

	legacy, err := secrets.Get(ctx, cfg.LegacyTokenSecretName, metav1.GetOptions{})
	if err == nil {
		legacyToken := string(legacy.Data[cfg.LegacyTokenSecretKey])
		if err := validateCredentialShape(legacyToken); err != nil {
			return "", "", fmt.Errorf("legacy durable agent credential has invalid shape: %w", err)
		}
		return legacyToken, credentialSourceLegacy, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", "", fmt.Errorf("read legacy durable agent credential: %w", err)
	}

	bootstrap, err := secrets.Get(ctx, cfg.BootstrapTokenSecretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", fmt.Errorf("active and legacy identities are empty and bootstrap credential is absent")
		}
		return "", "", fmt.Errorf("read bootstrap agent credential: %w", err)
	}
	bootstrapToken := string(bootstrap.Data[cfg.BootstrapTokenSecretKey])
	if err := validateCredentialShape(bootstrapToken); err != nil {
		return "", "", fmt.Errorf("bootstrap agent credential has invalid shape: %w", err)
	}
	return bootstrapToken, credentialSourceBootstrap, nil
}

func resolveLegacyCredential(ctx context.Context, secrets secretGetter, cfg *AgentConfig) (string, string, error) {
	legacy, err := secrets.Get(ctx, cfg.LegacyTokenSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("read image-first legacy agent credential: %w", err)
	}
	legacyToken := string(legacy.Data[cfg.LegacyTokenSecretKey])
	if err := validateCredentialShape(legacyToken); err != nil {
		return "", "", fmt.Errorf("image-first legacy agent credential has invalid shape: %w", err)
	}
	return legacyToken, credentialSourceLegacy, nil
}

func validateCredentialShape(token string) error {
	if token == "" {
		return fmt.Errorf("credential is empty")
	}
	if token != strings.TrimSpace(token) {
		return fmt.Errorf("credential contains surrounding whitespace")
	}
	if len(token) < 16 || len(token) > 4096 {
		return fmt.Errorf("credential length is outside the accepted range")
	}
	if strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("credential contains whitespace or control characters")
	}
	return nil
}

func serviceAccountNamespace() (string, error) {
	nsBytes, err := os.ReadFile(serviceAccountNamespacePath)
	if err != nil {
		return "", err
	}
	namespace := strings.TrimSpace(string(nsBytes))
	if namespace == "" {
		return "", fmt.Errorf("service-account namespace is empty")
	}
	return namespace, nil
}

func persistRotatedToken(ctx context.Context, cfg *AgentConfig, token string) error {
	if cfg == nil || token == "" {
		return fmt.Errorf("agent config and durable token are required")
	}
	if usesLegacyCredentialStorage(cfg) {
		if cfg.LegacyTokenSecretName == "" || cfg.LegacyTokenSecretKey == "" {
			return fmt.Errorf("legacy identity Secret name and key are required")
		}
	} else if cfg.IdentityTokenSecretName == "" || cfg.IdentityTokenSecretKey == "" {
		return fmt.Errorf("active identity Secret name and key are required")
	}
	namespace, err := serviceAccountNamespace()
	if err != nil {
		return fmt.Errorf("resolve token persistence namespace: %w", err)
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("initialize in-cluster token persistence: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("initialize Kubernetes token persistence client: %w", err)
	}
	return persistRotatedTokenWithClient(ctx, clientset, namespace, cfg, token)
}

// persistRotatedTokenWithClient owns only one data.<token-key> field. A current
// layout writes the pre-created active identity. An image-first legacy-layout
// process that authenticated with legacy durable identity patches that existing
// legacy Secret only; it never attempts identity migration until a current-
// manifest restart. Neither path performs Create or full-object Update.
func persistRotatedTokenWithClient(ctx context.Context, client kubernetes.Interface, namespace string, cfg *AgentConfig, token string) error {
	if client == nil || cfg == nil {
		return fmt.Errorf("Kubernetes token persistence client and agent config are required")
	}
	if err := validateCredentialShape(token); err != nil {
		return fmt.Errorf("refuse to persist invalid durable agent credential: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, credentialWriteTimeout)
	defer cancel()
	secrets := client.CoreV1().Secrets(namespace)
	if usesLegacyCredentialStorage(cfg) {
		if err := scrubLastAppliedAnnotation(writeCtx, secrets, cfg.LegacyTokenSecretName); err != nil {
			return fmt.Errorf("scrub image-first legacy identity annotation: %w", err)
		}
		return applyCredentialToken(writeCtx, secrets, namespace, cfg.LegacyTokenSecretName, cfg.LegacyTokenSecretKey, token, "image-first legacy identity")
	}
	if err := scrubLastAppliedAnnotation(writeCtx, secrets, cfg.IdentityTokenSecretName); err != nil {
		return fmt.Errorf("scrub active identity annotation: %w", err)
	}
	if err := scrubLastAppliedAnnotation(writeCtx, secrets, cfg.LegacyTokenSecretName); err != nil {
		return fmt.Errorf("scrub legacy identity annotation: %w", err)
	}

	return applyCredentialToken(writeCtx, secrets, namespace, cfg.IdentityTokenSecretName, cfg.IdentityTokenSecretKey, token, "durable identity")
}

func usesLegacyCredentialStorage(cfg *AgentConfig) bool {
	return cfg != nil && cfg.LegacyLayoutConfigured && cfg.CredentialSource == credentialSourceLegacy
}

func applyCredentialToken(ctx context.Context, secrets secretGetterPatcher, namespace, name, key, token, description string) error {
	apply := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{key: []byte(token)},
	}
	payload, err := json.Marshal(apply)
	if err != nil {
		return fmt.Errorf("encode %s apply patch: %w", description, err)
	}
	force := true
	if _, err := secrets.Patch(ctx, name, types.ApplyPatchType, payload, metav1.PatchOptions{
		FieldManager: credentialFieldManager,
		Force:        &force,
	}); err != nil {
		return fmt.Errorf("server-side apply %s token: %w", description, err)
	}
	return nil
}

type secretGetter interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.Secret, error)
}

type secretGetterPatcher interface {
	Get(context.Context, string, metav1.GetOptions) (*corev1.Secret, error)
	Patch(context.Context, string, types.PatchType, []byte, metav1.PatchOptions, ...string) (*corev1.Secret, error)
}

func scrubLastAppliedAnnotation(ctx context.Context, secrets secretGetterPatcher, name string) error {
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, ok := secret.Annotations[lastAppliedAnnotation]; !ok {
		return nil
	}
	// Merge-null is idempotent if another actor removes the annotation between
	// GET and PATCH. The static payload never copies or logs annotation content.
	patch := []byte(`{"metadata":{"annotations":{"kubectl.kubernetes.io/last-applied-configuration":null}}}`)
	_, err = secrets.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: credentialFieldManager})
	return err
}
