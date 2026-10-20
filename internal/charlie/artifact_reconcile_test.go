package charlie

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeActiveConnection struct {
	connection sqlc.CharlieConnection
	err        error
	updates    []sqlc.UpdateCharlieConnectionAppliedArtifactsParams
}

func (f *fakeActiveConnection) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, f.err
}

// UpdateCharlieConnectionAppliedArtifacts mirrors the sqlc query: the next
// GetActiveCharlieConnection returns the persisted digests.
func (f *fakeActiveConnection) UpdateCharlieConnectionAppliedArtifacts(_ context.Context, arg sqlc.UpdateCharlieConnectionAppliedArtifactsParams) (sqlc.CharlieConnection, error) {
	f.updates = append(f.updates, arg)
	f.connection.ChartReference = arg.ChartReference
	f.connection.ChartDigest = arg.ChartDigest
	f.connection.ImageReference = arg.ImageReference
	f.connection.ImageDigest = arg.ImageDigest
	return f.connection, nil
}

type recordingApplyHelm struct {
	specs []HelmReleaseSpec
}

func (r *recordingApplyHelm) Apply(_ context.Context, spec HelmReleaseSpec) error {
	r.specs = append(r.specs, spec)
	return nil
}

func (recordingApplyHelm) Uninstall(context.Context) error { return nil }

func TestArtifactReconcilerUpgradesWhenCharlieOffersNewDigest(t *testing.T) {
	const current = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const next = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const chart = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/charlie/v1/artifacts/release" {
			t.Fatalf("path %s", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "charlie" || password != "artifact-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(AdvertisedRelease{
			Schema: advertisedReleaseSchema, Image: "charlie.example/charlie/agent@" + next,
			ManifestDigest: next, Chart: "oci://charlie.example/charlie/agent-chart", ChartDigest: chart,
		})
	}))
	t.Cleanup(server.Close)

	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: artifactPullSecret, Namespace: agentNamespaceName},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"charlie.example":{"password":"artifact-secret"}}}`)},
	})
	helm := &recordingApplyHelm{}
	reconciler := NewArtifactReconciler(&fakeActiveConnection{connection: sqlc.CharlieConnection{
		Active: true, CentralUrl: server.URL, ImageDigest: current, ChartDigest: chart,
	}}, client, helm)
	reconciler.HTTP = server.Client()
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(helm.specs) != 1 || helm.specs[0].ImageDigest != next || !helm.specs[0].ReuseValues {
		t.Fatalf("upgrade specs %+v", helm.specs)
	}
}

func TestArtifactReconcilerSkipsMatchingPins(t *testing.T) {
	const pin = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const chart = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(AdvertisedRelease{
			Schema: advertisedReleaseSchema, Image: "charlie.example/charlie/agent@" + pin,
			ManifestDigest: pin, Chart: "oci://charlie.example/charlie/agent-chart", ChartDigest: chart,
		})
	}))
	t.Cleanup(server.Close)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: artifactPullSecret, Namespace: agentNamespaceName},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"charlie.example":{"password":"artifact-secret"}}}`)},
	})
	helm := &recordingApplyHelm{}
	reconciler := NewArtifactReconciler(&fakeActiveConnection{connection: sqlc.CharlieConnection{
		Active: true, CentralUrl: server.URL, ImageDigest: pin, ChartDigest: chart,
	}}, client, helm)
	reconciler.HTTP = server.Client()
	if err := reconciler.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(helm.specs) != 0 {
		t.Fatalf("unexpected upgrade %+v", helm.specs)
	}
}

// Regression for the P0 found at runtime: Reconcile applied the advertised
// release every tick because the applied digests were never written back,
// growing Helm history by one revision per interval. The fake store behaves
// like the sqlc query (the update is visible to the next read), so the second
// reconcile must observe matching pins and not call Helm again.
func TestArtifactReconcilerPersistsAppliedReleaseAndDoesNotReapply(t *testing.T) {
	const current = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const next = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const chart = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(AdvertisedRelease{
			Schema: advertisedReleaseSchema, Image: "charlie.example/charlie/agent@" + next,
			ManifestDigest: next, Chart: "oci://charlie.example/charlie/agent-chart", ChartDigest: chart,
		})
	}))
	t.Cleanup(server.Close)
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: artifactPullSecret, Namespace: agentNamespaceName},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"charlie.example":{"password":"artifact-secret"}}}`)},
	})
	helm := &recordingApplyHelm{}
	store := &fakeActiveConnection{connection: sqlc.CharlieConnection{
		Active: true, CentralUrl: server.URL, ImageDigest: current, ChartDigest: chart,
	}}
	reconciler := NewArtifactReconciler(store, client, helm)
	reconciler.HTTP = server.Client()

	for tick := 0; tick < 3; tick++ {
		if err := reconciler.Reconcile(context.Background()); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
	}
	if len(helm.specs) != 1 {
		t.Fatalf("expected exactly one Helm apply across three ticks, got %d: %+v", len(helm.specs), helm.specs)
	}
	if len(store.updates) != 1 {
		t.Fatalf("expected the applied release to be persisted once, got %d", len(store.updates))
	}
	applied := store.updates[0]
	if applied.ImageDigest != next || applied.ChartDigest != chart || applied.ChartReference != "oci://charlie.example/charlie/agent-chart" || applied.ImageReference != "charlie.example/charlie/agent@"+next {
		t.Fatalf("persisted release %+v", applied)
	}
}
