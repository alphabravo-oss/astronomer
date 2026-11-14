//go:build integration

package provisioning

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"sync"
	"testing"
	"time"

	"github.com/alphabravo-oss/identity/pkg/provision"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

var credentialKubeconfig = flag.String("identity-test-kubeconfig", "", "explicit Kubernetes target for isolated credential storage integration tests")

func credentialTestCipher(t *testing.T) *crypto.Encryptor {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func credentialTestCluster(t *testing.T) (kubernetes.Interface, string) {
	t.Helper()
	if *credentialKubeconfig == "" {
		t.Skip("requires explicit -identity-test-kubeconfig; no ambient cluster access")
	}
	config, err := clientcmd.BuildConfigFromFlags("", *credentialKubeconfig)
	if err != nil {
		t.Fatal("cannot load credential test kubeconfig")
	}
	config.Timeout = 15 * time.Second
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatal("cannot construct credential test Kubernetes client")
	}
	ns, err := client.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "identity-credential-test-", Labels: map[string]string{"astronomer.io/test": "identity-credentials"}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &ns.UID}}); err != nil {
			t.Error(err)
		}
	})
	return client, ns.Name
}

func TestCredentialSecretDurabilityAndIsolation(t *testing.T) {
	t.Parallel()
	client, namespace := credentialTestCluster(t)
	cipher := credentialTestCipher(t)
	scope := Scope{Issuer: "https://identity.example.test", ProjectID: uuid.New(), InstallationID: uuid.New(), Generation: 1}
	store, err := NewSecretStore(client.CoreV1(), namespace, scope, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing material did not fail closed")
	}
	result := provision.Result{OrgID: "org", ProjectID: "project", AppID: "app", ClientID: "client", ClientSecret: uuid.NewString(), Created: true}
	if err := store.Put(t.Context(), "another-project", result); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong shared project accepted")
	}
	assertConcurrentPuts(t, store, scope.ProjectName(), result)
	// A new adapter represents a process restart: durable data, not memory, wins.
	restarted, err := NewSecretStore(client.CoreV1(), namespace, scope, cipher)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Get(t.Context())
	if err != nil || got != durableResult(result) {
		t.Fatal("credential did not survive adapter restart")
	}
	secret, err := client.CoreV1().Secrets(namespace).Get(t.Context(), store.Name(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(secret.Data["credential.enc"], []byte(result.ClientSecret)) {
		t.Fatal("plaintext secret stored")
	}
	changed := result
	changed.ClientSecret = uuid.NewString()
	if err := store.Put(t.Context(), scope.ProjectName(), changed); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting credential overwrote original")
	}
	assertCredentialIsolation(t, client, namespace, scope, cipher, secret)
	got, err = store.Get(t.Context())
	if err != nil || got != durableResult(result) {
		t.Fatal("original credential changed")
	}
	for range 2 {
		if err := store.Delete(t.Context()); err != nil {
			t.Fatal("owned cleanup was not idempotent")
		}
	}
	if _, err := store.Get(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Fatal("owned cleanup left credential accessible")
	}
}

func assertConcurrentPuts(t *testing.T, store *SecretStore, project string, result provision.Result) {
	t.Helper()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := store.Put(t.Context(), project, result); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func assertCredentialIsolation(t *testing.T, client kubernetes.Interface, namespace string, scope Scope, cipher *crypto.Encryptor, original *corev1.Secret) {
	t.Helper()
	wrongKey, err := NewSecretStore(client.CoreV1(), namespace, scope, credentialTestCipher(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongKey.Get(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("wrong encryption key accepted")
	}
	for _, field := range []string{"issuer", "project", "installation", "generation"} {
		other := scope
		switch field {
		case "issuer":
			other.Issuer = "https://another.example.test"
		case "project":
			other.ProjectID = uuid.New()
		case "installation":
			other.InstallationID = uuid.New()
		case "generation":
			other.Generation++
		}
		store, err := NewSecretStore(client.CoreV1(), namespace, other, cipher)
		if err != nil {
			t.Fatal(err)
		}
		if store.Name() == original.Name {
			t.Fatal("credential scopes collided")
		}
		copy := original.DeepCopy()
		copy.ObjectMeta = metav1.ObjectMeta{Name: store.Name(), Namespace: namespace, Labels: map[string]string{
			"app.kubernetes.io/managed-by": "astronomer", "astronomer.io/identity-installation": other.InstallationID.String(), "astronomer.io/identity-project": other.ProjectID.String(),
		}}
		if _, err := client.CoreV1().Secrets(namespace).Create(t.Context(), copy, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(t.Context()); !errors.Is(err, ErrConflict) {
			t.Fatalf("copied ciphertext accepted for changed %s", field)
		}
		if err := store.Delete(t.Context()); !errors.Is(err, ErrConflict) {
			t.Fatalf("cleanup accepted copied ciphertext for changed %s", field)
		}
	}
}
