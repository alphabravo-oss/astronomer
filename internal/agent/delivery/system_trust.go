package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TrustBundleConfigMap holds the agent's own CA bundle for the Flux
// source-controller (the only controller that fetches from remote origins).
const TrustBundleConfigMap = "astronomer-trust-bundle"

const trustBundleKey = "ca-certificates.crt"

// trustBundlePath is the agent image's system CA bundle (distroless). A var so
// tests can point it elsewhere.
var trustBundlePath = "/etc/ssl/certs/ca-certificates.crt"

// configureTrustBundle replaces the CA directory of the upstream Flux
// source-controller image with the agent image's bundle. Upstream Flux images
// can lag on trust-store and Go fixes (2026-10-20: source-controller is built
// with go1.26.8 and its image still trusts emSign ECC Root CA - G3 after the
// eMudhra intermediate compromise). Mounting the whole directory matters: Go
// reads every file under /etc/ssl/certs, so SSL_CERT_FILE alone is not
// enough. Expressed as a Flux patch on the system Kustomization, so the signed
// artifact stays unchanged and reconciliation keeps the mount. Missing bundle
// (non-distroless dev build) leaves the upstream image's trust untouched.
func (m *SystemManager) configureTrustBundle(ctx context.Context, objects []*unstructured.Unstructured) error {
	bundle, err := os.ReadFile(trustBundlePath)
	if err != nil || len(bundle) == 0 {
		return nil //nolint:nilerr // no local bundle: keep the controller image's own trust store
	}
	sum := sha256.Sum256(bundle)
	digest := hex.EncodeToString(sum[:])
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: TrustBundleConfigMap, Namespace: DeliverySystemNamespace,
			Labels:      map[string]string{"app.kubernetes.io/managed-by": "astronomer-agent"},
			Annotations: map[string]string{"astronomer.io/trust-bundle-sha256": digest},
		},
		Data: map[string]string{trustBundleKey: string(bundle)},
	}
	if err := m.writeTrustBundle(ctx, desired); err != nil {
		// Agents enrolled before the trust-bundle Role existed cannot write the
		// ConfigMap. Never mount a volume that may not exist (the controller
		// pod would not start): keep the image's trust and let delivery go on.
		if apierrors.IsForbidden(err) {
			slog.WarnContext(ctx, "delivery trust bundle not written; source-controller keeps its image trust store",
				"event", "delivery_trust_bundle_forbidden", "namespace", DeliverySystemNamespace, "error", err.Error())
			return nil
		}
		return err
	}
	patch := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: source-controller
spec:
  template:
    metadata:
      annotations:
        astronomer.io/trust-bundle-sha256: %q
    spec:
      containers:
        - name: manager
          volumeMounts:
            - name: astronomer-trust-bundle
              mountPath: /etc/ssl/certs
              readOnly: true
      volumes:
        - name: astronomer-trust-bundle
          configMap:
            name: %s
`, digest, TrustBundleConfigMap)
	for _, object := range objects {
		if object.GroupVersionKind() != kustomizationGVK {
			continue
		}
		patches, _, err := unstructured.NestedSlice(object.Object, "spec", "patches")
		if err != nil {
			return err
		}
		patches = append(patches, map[string]any{
			"target": map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "name": "source-controller"},
			"patch":  patch,
		})
		if err := unstructured.SetNestedSlice(object.Object, patches, "spec", "patches"); err != nil {
			return err
		}
	}
	return nil
}

func (m *SystemManager) writeTrustBundle(ctx context.Context, desired *corev1.ConfigMap) error {
	configMaps := m.client.CoreV1().ConfigMaps(DeliverySystemNamespace)
	current, err := configMaps.Get(ctx, TrustBundleConfigMap, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		if _, err := configMaps.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("create delivery trust bundle: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read delivery trust bundle: %w", err)
	case current.Data[trustBundleKey] != desired.Data[trustBundleKey]:
		current.Data, current.Annotations = desired.Data, desired.Annotations
		if _, err := configMaps.Update(ctx, current, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update delivery trust bundle: %w", err)
		}
	}
	return nil
}
