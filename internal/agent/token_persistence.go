package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	serviceAccountNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	credentialReadTimeout       = 10 * time.Second
	credentialSourceDurable     = "durable_secret"
	credentialSourceBootstrap   = "bootstrap_secret"
	credentialSourceEnvironment = "environment"
)

// resolveStartupCredential prefers agent-owned durable identity. The
// installer-provided environment value is bootstrap-only and is selected only
// when the durable Secret is authoritatively NotFound. Any other Kubernetes
// read failure is fail-closed so an RBAC/API outage cannot silently downgrade a
// previously adopted agent to a replayable registration credential.
func resolveStartupCredential(ctx context.Context, cfg *AgentConfig, log *slog.Logger) error {
	if cfg == nil {
		return fmt.Errorf("agent config is required")
	}
	if log == nil {
		log = slog.Default()
	}
	// Preserve the supported off-cluster/helm-only development path. A real pod
	// always receives KUBERNETES_SERVICE_HOST from Kubernetes service discovery.
	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) == "" {
		cfg.AgentToken = strings.TrimSpace(cfg.AgentToken)
		cfg.CredentialSource = credentialSourceEnvironment
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
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, cfg.DurableTokenSecretName, metav1.GetOptions{})
	if err == nil {
		token := string(secret.Data[cfg.DurableTokenSecretKey])
		if err := validateCredentialShape(token); err != nil {
			return "", "", fmt.Errorf("durable agent credential has invalid shape: %w", err)
		}
		return token, credentialSourceDurable, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", "", fmt.Errorf("read durable agent credential: %w", err)
	}
	bootstrap := strings.TrimSpace(cfg.AgentToken)
	if err := validateCredentialShape(bootstrap); err != nil {
		return "", "", fmt.Errorf("durable agent credential is absent and bootstrap credential has invalid shape: %w", err)
	}
	return bootstrap, credentialSourceBootstrap, nil
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

// persistRotatedToken writes CONNECT_ACK rotations only to the agent-owned
// durable Secret. The installer-owned bootstrap Secret is never read, patched,
// updated, or deleted by this path.
func persistRotatedToken(ctx context.Context, cfg *AgentConfig, token string) error {
	if cfg == nil || token == "" || cfg.DurableTokenSecretName == "" || cfg.DurableTokenSecretKey == "" {
		return nil
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

func persistRotatedTokenWithClient(ctx context.Context, client kubernetes.Interface, namespace string, cfg *AgentConfig, token string) error {
	if client == nil || cfg == nil {
		return fmt.Errorf("Kubernetes token persistence client and agent config are required")
	}
	if err := validateCredentialShape(token); err != nil {
		return fmt.Errorf("refuse to persist invalid durable agent credential: %w", err)
	}
	secrets := client.CoreV1().Secrets(namespace)
	secret, err := secrets.Get(ctx, cfg.DurableTokenSecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cfg.DurableTokenSecretName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":       "astronomer-agent",
					"app.kubernetes.io/component":  "agent",
					"app.kubernetes.io/part-of":    "astronomer",
					"app.kubernetes.io/managed-by": "astronomer-agent",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{cfg.DurableTokenSecretKey: []byte(token)},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("create durable token secret: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read durable token secret for update: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[cfg.DurableTokenSecretKey] = []byte(token)
	delete(secret.StringData, cfg.DurableTokenSecretKey)
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update durable token secret: %w", err)
	}
	return nil
}
