package charlie

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testAgentSecretBundle(name, marker string) AgentSecretBundle {
	return AgentSecretBundle{
		Name: name, OnboardingPackage: []byte(`{"schema":"charlie.onboarding/v1","marker":"` + marker + `"}`),
		ArtifactPullCredential: "artifact-" + marker, CACertificatePEM: "ca-" + marker,
		BridgeServerCertificate: "bridge-cert-" + marker, BridgeServerPrivateKey: "bridge-key-" + marker,
		MCPClientCertificate: "mcp-cert-" + marker, MCPClientPrivateKey: "mcp-key-" + marker,
	}
}

func TestKubernetesAgentSecretWriterCreateAndRollback(t *testing.T) {
	client := fake.NewClientset()
	writer, err := NewKubernetesAgentSecretWriter(client, "astronomer-charlie", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	bundle := testAgentSecretBundle("bootstrap", "first")
	bundle.InstallationID = "3c608d44-848c-45d6-bd86-246be0b880af"
	receipt, err := writer.WriteAgentSecret(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.IntegrityHMAC) != 64 || receipt.Rollback == nil {
		t.Fatalf("unsafe receipt: %+v", receipt)
	}
	secret, err := client.CoreV1().Secrets("astronomer-charlie").Get(context.Background(), "bootstrap", metav1.GetOptions{})
	if err != nil || string(secret.Data["onboarding-package.json"]) != `{"schema":"charlie.onboarding/v1","marker":"first"}` {
		t.Fatalf("Secret was not materialized directly: secret=%+v err=%v", secret, err)
	}
	if secret.Labels[installationOwnerLabel] != bundle.InstallationID {
		t.Fatal("bootstrap Secret is not owner-marked")
	}
	if err := receipt.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().Secrets("astronomer-charlie").Get(context.Background(), "bootstrap", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("created Secret survived rollback: %v", err)
	}
}

func TestKubernetesAgentSecretWriterRetriesConflictAndRestoresPrevious(t *testing.T) {
	previous := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", Namespace: "astronomer-charlie", Labels: map[string]string{"owner": "operator"}}, Data: map[string][]byte{"old": []byte("value")}}
	client := fake.NewClientset(previous)
	conflicts := 0
	client.PrependReactor("update", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts == 0 {
			conflicts++
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, "bootstrap", context.DeadlineExceeded)
		}
		return false, nil, nil
	})
	writer, err := NewKubernetesAgentSecretWriter(client, "astronomer-charlie", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := writer.WriteAgentSecret(context.Background(), testAgentSecretBundle("bootstrap", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if conflicts != 1 {
		t.Fatalf("conflict retries=%d, want 1", conflicts)
	}
	if err := receipt.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := client.CoreV1().Secrets("astronomer-charlie").Get(context.Background(), "bootstrap", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(restored.Data["old"]) != "value" || restored.Labels["owner"] != "operator" || len(restored.Data) != 1 {
		t.Fatalf("rollback did not restore exact previous Secret: %+v", restored)
	}
}
