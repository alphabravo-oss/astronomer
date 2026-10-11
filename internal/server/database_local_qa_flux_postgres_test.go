//go:build unix && integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	digest "github.com/opencontainers/go-digest"
	"github.com/sigstore/sigstore-go/pkg/root"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const localQAFluxNamespace = "astronomer-delivery-system"
const localQAFluxSourceName = "astronomer-system-release"
const localQAFluxOwner = "astronomer-owned-local-qa-certificate"

// This is explicitly selected fixture authority from ROOT's leased real
// kubeconfig. No production object builder or verifier is replaced. It only
// adds supported native custom-root/TLS/pull refs to the genuine agent's source;
// all application-owned fields must remain unchanged. No fake status is written.
func localQAConfigureNativeFlux(t *testing.T, ctx context.Context, cfg *config.Config) {
	t.Helper()
	if cfg == nil || !cfg.DeliveryEnabled || !cfg.DeliveryDatabaseUpdatesEnabled || cfg.DeliverySourceTrustDirectory == "" || cfg.DeliveryDatabaseRegistryRootsRef == "" || cfg.DeliveryDatabaseReadCredentialRef == "" || cfg.DeliveryFluxDistributionPublicKey != "" || cfg.DeliveryFluxDistributionPublicKeys != "" || len(cfg.DeliveryFluxDistributionKeyring) != 0 {
		t.Fatal("LOCAL QA native Flux requires actual keyless typed configuration")
	}
	registry, _, ok := strings.Cut(cfg.DeliveryFluxDistributionRepository, "/")
	if !ok || registry != cfg.DeliveryDatabaseReadRegistry {
		t.Fatal("LOCAL QA native Flux pull scope differs from actual selected registry")
	}
	expectedURL := "oci://" + cfg.DeliveryFluxDistributionRepository
	rootPath := filepath.Join(cfg.DeliverySourceTrustDirectory, "trusted_root.json")
	caPath := filepath.Join(cfg.DeliverySourceTrustDirectory, cfg.DeliveryDatabaseRegistryRootsRef+".pem")
	pullPath := filepath.Join(cfg.DeliverySourceTrustDirectory, cfg.DeliveryDatabaseReadCredentialRef+".json")
	rootBytes := localQAReadPrivateInput(t, rootPath, 1<<20)
	defer clear(rootBytes)
	trusted, err := root.NewTrustedRootFromJSON(rootBytes)
	if err != nil || len(trusted.FulcioCertificateAuthorities()) == 0 || len(trusted.CTLogs()) == 0 || len(trusted.RekorLogs()) != 1 {
		t.Fatal("LOCAL QA native Flux requires the exact owned complete certificate root")
	}
	// A valid embedded legacy SET verifies against this root without a public
	// TUF fetch or Rekor lookup. Actual network denial/counters must qualify that
	// behavior; presence of a client BaseURL alone is not evidence of a server.
	for _, log := range trusted.RekorLogs() {
		parsed, e := url.Parse(log.BaseURL)
		if e != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			t.Fatal("LOCAL QA native Flux root has no bounded TLS log origin")
		}
	}
	caBytes := localQAReadPrivateInput(t, caPath, 256<<10)
	defer clear(caBytes)
	privateBytes := localQAReadPrivateInput(t, pullPath, 32<<10)
	defer clear(privateBytes)
	var credential localQARuntimePush
	if jsoncontract.Decode(privateBytes, &credential) != nil || credential.SchemaVersion != 1 || credential.Username == "" || credential.Password == "" || len(credential.Username) > 4096 || len(credential.Password) > 16384 || strings.ContainsAny(credential.Username, "\r\n\x00:") || strings.ContainsAny(credential.Password, "\r\n\x00") {
		t.Fatal("LOCAL QA native Flux independent read credential invalid")
	}
	defer func() { credential.Username = ""; credential.Password = "" }()
	dockerBytes, err := json.Marshal(struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"auths"`
	}{Auths: map[string]struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{registry: {Username: credential.Username, Password: credential.Password}}})
	if err != nil {
		t.Fatal("LOCAL QA native Flux pull encoding failed")
	}
	defer clear(dockerBytes)
	client, input := localQAKubernetes(t)
	localQAObserveSelectedFluxController(t, ctx, client)
	kubeBytes := localQAReadPrivateInput(t, input.Kubeconfig, 1<<20)
	defer clear(kubeBytes)
	rest, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	if err != nil {
		t.Fatal("LOCAL QA native Flux kubeconfig invalid")
	}
	rest.Timeout = 20 * time.Second
	dynamicClient, err := dynamic.NewForConfig(rest)
	if err != nil {
		t.Fatal("LOCAL QA native Flux API unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resource := dynamicClient.Resource(schema.GroupVersionResource{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"}).Namespace(localQAFluxNamespace)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var source *unstructured.Unstructured
	for {
		source, err = resource.Get(bounded, localQAFluxSourceName, metav1.GetOptions{})
		if err == nil {
			break
		}
		if !apierrors.IsNotFound(err) {
			t.Fatal("LOCAL QA genuine Flux source read failed")
		}
		select {
		case <-bounded.Done():
			t.Fatal("LOCAL QA genuine agent did not create Flux source")
		case <-ticker.C:
		}
	}
	localQAValidateFluxSource(t, source, cfg, expectedURL)
	uid := source.GetUID()
	beforeSpec, found, err := unstructured.NestedMap(source.Object, "spec")
	if err != nil || !found {
		t.Fatal("LOCAL QA genuine source spec missing")
	}
	for _, name := range []string{"trustedRootSecretRef", "certSecretRef"} {
		fields := []string{"spec", name}
		if name == "trustedRootSecretRef" {
			fields = []string{"spec", "verify", name}
		}
		if _, exists, _ := unstructured.NestedFieldNoCopy(source.Object, fields...); exists {
			t.Fatal("LOCAL QA native root/TLS fields already owned; refusing adoption")
		}
	}
	if _, exists, _ := unstructured.NestedFieldNoCopy(source.Object, "spec", "secretRef"); exists {
		t.Fatal("LOCAL QA native fixture refuses to replace existing source pull authority")
	}
	secrets := []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-local-qa-certificate-root", Namespace: localQAFluxNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": localQAFluxOwner}}, Immutable: localQABool(true), Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"trusted_root.json": rootBytes}},
		{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-local-qa-registry-ca", Namespace: localQAFluxNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": localQAFluxOwner}}, Immutable: localQABool(true), Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"ca.crt": caBytes}},
		{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-local-qa-registry-pull", Namespace: localQAFluxNamespace, Labels: map[string]string{"app.kubernetes.io/managed-by": localQAFluxOwner}}, Immutable: localQABool(true), Type: corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: dockerBytes}},
	}
	// Create-only: existing foreign or prior fixture Secrets do not get adopted.
	// ROOT retains this exclusive cluster on failure; cleanup is a separately
	// scoped owner operation with exact UID preconditions, never broad deletion.
	for _, secret := range secrets {
		created, e := client.CoreV1().Secrets(localQAFluxNamespace).Create(bounded, secret, metav1.CreateOptions{FieldManager: localQAFluxOwner})
		if e != nil || created.UID == "" || created.DeletionTimestamp != nil || created.Immutable == nil || !*created.Immutable || created.Type != secret.Type || !reflect.DeepEqual(created.Data, secret.Data) {
			t.Fatal("LOCAL QA native Flux exclusive Secret creation/readback failed")
		}
		secret.UID = created.UID
	}
	patch, err := json.Marshal(map[string]any{"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "OCIRepository", "metadata": map[string]any{"name": localQAFluxSourceName, "namespace": localQAFluxNamespace, "uid": uid, "resourceVersion": source.GetResourceVersion()}, "spec": map[string]any{"verify": map[string]any{"trustedRootSecretRef": map[string]any{"name": secrets[0].Name}}, "certSecretRef": map[string]any{"name": secrets[1].Name}, "secretRef": map[string]any{"name": secrets[2].Name}}})
	if err != nil {
		t.Fatal("LOCAL QA native Flux exact overlay encoding failed")
	}
	force := false
	if _, err = resource.Patch(bounded, localQAFluxSourceName, types.ApplyPatchType, patch, metav1.PatchOptions{FieldManager: localQAFluxOwner, Force: &force}); err != nil {
		t.Fatal("LOCAL QA native Flux supported overlay failed; no force/adoption fallback")
	}
	var readyAt time.Time
	for {
		current, e := resource.Get(bounded, localQAFluxSourceName, metav1.GetOptions{})
		if e != nil || current.GetUID() != uid {
			t.Fatal("LOCAL QA genuine Flux source UID changed")
		}
		localQAValidateFluxSource(t, current, cfg, expectedURL)
		spec, present, e := unstructured.NestedMap(current.Object, "spec")
		if e != nil || !present {
			t.Fatal("LOCAL QA actual Flux spec missing")
		}
		verify, ok := spec["verify"].(map[string]any)
		if !ok || !reflect.DeepEqual(verify["trustedRootSecretRef"], map[string]any{"name": secrets[0].Name}) || !reflect.DeepEqual(spec["certSecretRef"], map[string]any{"name": secrets[1].Name}) || !reflect.DeepEqual(spec["secretRef"], map[string]any{"name": secrets[2].Name}) {
			t.Fatal("LOCAL QA exact supported Flux refs changed")
		}
		delete(verify, "trustedRootSecretRef")
		delete(spec, "certSecretRef")
		delete(spec, "secretRef")
		if !reflect.DeepEqual(spec, beforeSpec) {
			t.Fatal("LOCAL QA native trust overlay changed agent-owned source fields")
		}
		for _, expected := range secrets {
			actual, e := client.CoreV1().Secrets(localQAFluxNamespace).Get(bounded, expected.Name, metav1.GetOptions{})
			if e != nil || actual.UID != expected.UID || actual.DeletionTimestamp != nil || actual.Immutable == nil || !*actual.Immutable || actual.Type != expected.Type || !reflect.DeepEqual(actual.Data, expected.Data) {
				t.Fatal("LOCAL QA native Flux trust/pull Secret replaced")
			}
		}
		for path, expected := range map[string][]byte{rootPath: rootBytes, caPath: caBytes, pullPath: privateBytes} {
			fresh := localQAReadPrivateInput(t, path, 1<<20)
			same := bytes.Equal(fresh, expected)
			clear(fresh)
			if !same {
				t.Fatal("LOCAL QA upstream mounted trust/pull rotated")
			}
		}
		observed, _, _ := unstructured.NestedInt64(current.Object, "status", "observedGeneration")
		revision, _, _ := unstructured.NestedString(current.Object, "status", "artifact", "revision")
		verified, ready := false, false
		conditions, _, _ := unstructured.NestedSlice(current.Object, "status", "conditions")
		for _, value := range conditions {
			condition, ok := value.(map[string]any)
			if !ok {
				t.Fatal("LOCAL QA actual Flux conditions malformed")
			}
			if condition["status"] == "True" && condition["observedGeneration"] == current.GetGeneration() {
				if condition["type"] == "SourceVerified" {
					verified = true
				}
				if condition["type"] == "Ready" {
					ready = true
				}
			}
		}
		if observed == current.GetGeneration() && verified && ready && revision == cfg.DeliveryFluxDistributionDigest {
			if readyAt.IsZero() {
				readyAt = time.Now()
			}
			if time.Since(readyAt) >= 5*time.Second {
				return
			}
		} else {
			readyAt = time.Time{}
		}
		select {
		case <-bounded.Done():
			t.Fatal("LOCAL QA genuine Flux certificate/artifact readiness failed; no fabricated readiness")
		case <-ticker.C:
		}
	}
}
func localQABool(value bool) *bool { return &value }
func localQAValidateFluxSource(t *testing.T, source *unstructured.Unstructured, cfg *config.Config, expectedURL string) {
	t.Helper()
	if source == nil || source.GetUID() == "" || source.GetResourceVersion() == "" || source.GetDeletionTimestamp() != nil || source.GetAPIVersion() != "source.toolkit.fluxcd.io/v1" || source.GetKind() != "OCIRepository" || source.GetName() != localQAFluxSourceName || source.GetNamespace() != localQAFluxNamespace || source.GetLabels()["app.kubernetes.io/managed-by"] != "astronomer-agent" || source.GetLabels()["delivery.astronomer.io/system"] != "true" {
		t.Fatal("LOCAL QA cannot adopt foreign/recreated Flux source")
	}
	actualURL, _, _ := unstructured.NestedString(source.Object, "spec", "url")
	actualDigest, _, _ := unstructured.NestedString(source.Object, "spec", "ref", "digest")
	provider, _, _ := unstructured.NestedString(source.Object, "spec", "verify", "provider")
	insecure, _, _ := unstructured.NestedBool(source.Object, "spec", "insecure")
	suspended, _, _ := unstructured.NestedBool(source.Object, "spec", "suspend")
	if actualURL != expectedURL || actualDigest != cfg.DeliveryFluxDistributionDigest || provider != "cosign" || insecure || suspended {
		t.Fatal("LOCAL QA genuine Flux immutable TLS source differs")
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(source.Object, "spec", "verify", "secretRef"); found {
		t.Fatal("LOCAL QA cannot substitute keyed verification for certificate verification")
	}
	identities, found, err := unstructured.NestedSlice(source.Object, "spec", "verify", "matchOIDCIdentity")
	if err != nil || !found || len(identities) != 1 || !reflect.DeepEqual(identities[0], map[string]any{"issuer": cfg.DeliveryFluxDistributionOIDCIssuer, "subject": cfg.DeliveryFluxDistributionCertificateIdentity}) {
		t.Fatal("LOCAL QA genuine Flux certificate identity changed")
	}
}

// The real selected controller image must already be installed independently
// from its retained selected OCI platform evidence. A SourceVerified flag from
// an unrelated controller cannot qualify the selected implementation.
func localQAObserveSelectedFluxController(t *testing.T, ctx context.Context, client kubernetes.Interface) {
	t.Helper()
	var input struct {
		Scope                  string `json:"scope"`
		SourceCommit           string `json:"source_commit"`
		ControllerChildDigest  string `json:"controller_child_digest"`
		ControllerConfigDigest string `json:"controller_config_digest"`
	}
	bytes := localQAReadPrivateInput(t, os.Getenv("W04_LOCAL_QA_FLUX_INPUT"), 16<<10)
	defer clear(bytes)
	if jsoncontract.Decode(bytes, &input) != nil || input.Scope != localQARuntimeLabel || input.SourceCommit != localQACandidateRuntimeCompiledSource(t) {
		t.Fatal("LOCAL QA selected Flux platform evidence unavailable")
	}
	for _, value := range []string{input.ControllerChildDigest, input.ControllerConfigDigest} {
		d := digest.Digest(value)
		if d.Validate() != nil || d.Algorithm() != digest.SHA256 {
			t.Fatal("LOCAL QA selected Flux platform identity invalid")
		}
	}
	selected, e := fluxdistribution.ControllerImages()
	if e != nil || selected["source-controller"].Version != "v1.9.6" {
		t.Fatal("LOCAL QA native custom-root path selected controller differs from reviewed implementation")
	}
	controller := selected["source-controller"]
	separator := strings.LastIndex(controller.Reference, ":")
	if separator <= strings.LastIndex(controller.Reference, "/") {
		t.Fatal("LOCAL QA canonical controller repository invalid")
	}
	repository := controller.Reference[:separator]
	image := controller.Reference + "@" + controller.Digest
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deployment, err := client.AppsV1().Deployments(localQAFluxNamespace).Get(bounded, "source-controller", metav1.GetOptions{})
	if err != nil || deployment.UID == "" || deployment.DeletionTimestamp != nil || deployment.Status.ObservedGeneration != deployment.Generation || deployment.Status.ReadyReplicas < 1 {
		t.Fatal("LOCAL QA selected source controller is not installed/current")
	}
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil || selector.Empty() {
		t.Fatal("LOCAL QA selected controller Pod selector invalid")
	}
	pods, err := client.CoreV1().Pods(localQAFluxNamespace).List(bounded, metav1.ListOptions{LabelSelector: selector.String(), Limit: 4})
	if err != nil || pods.Continue != "" || len(pods.Items) == 0 || len(pods.Items) > 4 {
		t.Fatal("LOCAL QA selected controller Pod inventory incomplete")
	}
	measured := false
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			continue
		}
		if pod.Status.Phase != corev1.PodRunning || pod.UID == "" {
			t.Fatal("LOCAL QA selected controller Pod not running")
		}
		found := false
		for _, container := range pod.Spec.Containers {
			if container.Name == "manager" {
				if found || container.Image != image {
					t.Fatal("LOCAL QA wrong source controller image")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("LOCAL QA selected source controller container missing")
		}
		ready := false
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == "manager" {
				if ready || !status.Ready || status.State.Running == nil || !localQARuntimeImage(status.ImageID, repository, input.ControllerChildDigest, input.ControllerConfigDigest) {
					t.Fatal("LOCAL QA selected Flux runtime imageID differs")
				}
				ready = true
			}
		}
		if !ready {
			t.Fatal("LOCAL QA selected source controller runtime identity absent")
		}
		measured = true
	}
	if !measured {
		t.Fatal("LOCAL QA selected source controller has no current Pod")
	}
}
