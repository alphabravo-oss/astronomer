package charlie

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type AgentSecretBundle struct {
	Name                    string
	InstallationID          string
	OnboardingPackage       []byte
	ArtifactPullCredential  string
	CACertificatePEM        string
	BridgeServerCertificate string
	BridgeServerPrivateKey  string
	MCPClientCertificate    string
	MCPClientPrivateKey     string
}

type SecretWriteReceipt struct {
	IntegrityHMAC string
	Rollback      func(context.Context) error
}

type AgentSecretWriter interface {
	WriteAgentSecret(context.Context, AgentSecretBundle) (SecretWriteReceipt, error)
}

type KubernetesAgentSecretWriter struct {
	client       kubernetes.Interface
	namespace    string
	integrityKey []byte
}

func NewKubernetesAgentSecretWriter(client kubernetes.Interface, namespace string, integrityKey []byte) (*KubernetesAgentSecretWriter, error) {
	if client == nil || !validServiceDNS("service."+strings.TrimSpace(namespace)+".svc") || len(integrityKey) < 32 {
		return nil, fmt.Errorf("Charlie Kubernetes Secret writer configuration is invalid")
	}
	return &KubernetesAgentSecretWriter{client: client, namespace: namespace, integrityKey: append([]byte(nil), integrityKey...)}, nil
}

func (w *KubernetesAgentSecretWriter) WriteAgentSecret(ctx context.Context, bundle AgentSecretBundle) (SecretWriteReceipt, error) {
	if w == nil || strings.TrimSpace(bundle.Name) == "" {
		return SecretWriteReceipt{}, fmt.Errorf("Charlie agent Secret target is invalid")
	}
	data := map[string][]byte{
		"onboarding-package.json": append([]byte(nil), bundle.OnboardingPackage...),
		"artifact-pull":           []byte(bundle.ArtifactPullCredential),
		"ca.crt":                  []byte(bundle.CACertificatePEM),
		"bridge-server.crt":       []byte(bundle.BridgeServerCertificate),
		"bridge-server.key":       []byte(bundle.BridgeServerPrivateKey),
		"mcp-client.crt":          []byte(bundle.MCPClientCertificate),
		"mcp-client.key":          []byte(bundle.MCPClientPrivateKey),
	}
	for key, value := range data {
		if len(value) == 0 {
			return SecretWriteReceipt{}, fmt.Errorf("Charlie agent Secret field %s is empty", key)
		}
	}
	integrity := secretHMAC(w.integrityKey, data)
	secrets := w.client.CoreV1().Secrets(w.namespace)
	previous, err := secrets.Get(ctx, bundle.Name, metav1.GetOptions{})
	created := false
	if apierrors.IsNotFound(err) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: bundle.Name, Namespace: w.namespace, Labels: agentSecretLabels(bundle.InstallationID)},
			Type:       corev1.SecretTypeOpaque, Data: data,
		}
		if _, err := secrets.Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return SecretWriteReceipt{}, fmt.Errorf("write Charlie agent Secret: %w", err)
		}
		created = true
	} else if err != nil {
		return SecretWriteReceipt{}, fmt.Errorf("read Charlie agent Secret: %w", err)
	} else {
		previous = previous.DeepCopy()
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, getErr := secrets.Get(ctx, bundle.Name, metav1.GetOptions{})
			if getErr != nil {
				return getErr
			}
			current.Type = corev1.SecretTypeOpaque
			current.Data = data
			if current.Labels == nil {
				current.Labels = map[string]string{}
			}
			current.Labels["app.kubernetes.io/managed-by"] = "astronomer"
			current.Labels["app.kubernetes.io/part-of"] = "charlie"
			if strings.TrimSpace(bundle.InstallationID) != "" {
				current.Labels[installationOwnerLabel] = strings.TrimSpace(bundle.InstallationID)
			}
			_, updateErr := secrets.Update(ctx, current, metav1.UpdateOptions{})
			return updateErr
		})
		if err != nil {
			return SecretWriteReceipt{}, fmt.Errorf("update Charlie agent Secret: %w", err)
		}
	}

	rollback := func(rollbackCtx context.Context) error {
		if created {
			err := secrets.Delete(rollbackCtx, bundle.Name, metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, getErr := secrets.Get(rollbackCtx, bundle.Name, metav1.GetOptions{})
			if getErr != nil {
				return getErr
			}
			restored := previous.DeepCopy()
			restored.ResourceVersion = current.ResourceVersion
			_, updateErr := secrets.Update(rollbackCtx, restored, metav1.UpdateOptions{})
			return updateErr
		})
	}
	return SecretWriteReceipt{IntegrityHMAC: integrity, Rollback: rollback}, nil
}

func agentSecretLabels(installationID string) map[string]string {
	labels := map[string]string{"app.kubernetes.io/managed-by": "astronomer", "app.kubernetes.io/part-of": "charlie"}
	if strings.TrimSpace(installationID) != "" {
		labels[installationOwnerLabel] = strings.TrimSpace(installationID)
	}
	return labels
}

func secretHMAC(key []byte, data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for name := range data {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	mac := hmac.New(sha256.New, key)
	for _, name := range keys {
		_, _ = mac.Write([]byte(name))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write(data[name])
		_, _ = mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}
