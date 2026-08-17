package delivery

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/google/uuid"
)

func TestKubernetesCheckpointStoreRoundTripShardsWithoutSensitiveAssignmentData(t *testing.T) {
	client := fake.NewClientset()
	store, err := NewKubernetesCheckpointStore(client, "astronomer-system")
	if err != nil {
		t.Fatal(err)
	}
	want := emptyCheckpoint()
	want.SnapshotGeneration = 42
	want.SnapshotETag = testDigest
	want.CredentialEpoch = 9
	for index := 0; index < 300; index++ {
		assignment := gitAssignment()
		assignment.DeploymentID = uuid.NewString()
		names := Names(assignment.ProjectID, assignment.DeploymentID)
		assignment.Source.CredentialSecret = names.AuthSecret
		assignment.Source.TrustSecret = names.TrustSecret
		assignment.Renderer.Kustomize.ServiceAccount = names.Applier
		materialization, buildErr := BuildAssignment(assignment, testCapabilities(), ValidationPolicy{})
		if buildErr != nil {
			t.Fatalf("build assignment %d: %v", index, buildErr)
		}
		want.Assignments[assignment.DeploymentID] = AcceptAssignment(assignment, materialization)
	}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	objects, err := client.CoreV1().ConfigMaps("astronomer-system").List(context.Background(), metav1.ListOptions{LabelSelector: checkpointLabel + "=true"})
	if err != nil {
		t.Fatal(err)
	}
	if len(objects.Items) < 3 {
		t.Fatalf("expected summary plus multiple shards, got %d ConfigMaps", len(objects.Items))
	}
	for _, object := range objects.Items {
		payload := object.Data[checkpointDataKey]
		for _, forbidden := range []string{"PRIVATE", "known_hosts", "example.test/platform/apps.git", "clusters/base", "ENVIRONMENT", "replicas"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("checkpoint %q leaked forbidden assignment data %q", object.Name, forbidden)
			}
		}
	}
	got, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("checkpoint did not round-trip")
	}
}

func TestKubernetesCheckpointStoreFailsClosedOnUnownedOrCorruptObjects(t *testing.T) {
	t.Run("unowned", func(t *testing.T) {
		client := fake.NewClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: checkpointName, Namespace: "astronomer-system"}})
		store, _ := NewKubernetesCheckpointStore(client, "astronomer-system")
		if err := store.Save(context.Background(), emptyCheckpoint()); err == nil || !strings.Contains(err.Error(), "unowned") {
			t.Fatalf("expected unowned ConfigMap refusal, got %v", err)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		client := fake.NewClientset(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: checkpointName, Namespace: "astronomer-system", Labels: map[string]string{checkpointLabel: "true"}},
			Data:       map[string]string{checkpointDataKey: `{"schema_version":1,"snapshot_generation":0,"credential_epoch":0} trailing`},
		})
		store, _ := NewKubernetesCheckpointStore(client, "astronomer-system")
		if _, err := store.Load(context.Background()); err == nil {
			t.Fatal("expected corrupt checkpoint to fail closed")
		}
	})
}
