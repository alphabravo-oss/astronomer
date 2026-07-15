package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// setStateSubscriberCRDTunables tightens the CRD retry/sync knobs so the
// discover-if-present tests finish quickly.
func setStateSubscriberCRDTunables(retry, syncTimeout time.Duration) func() {
	prevRetry := stateSubscriberCRDRetry.Load()
	prevSync := stateSubscriberCRDSyncTimeout.Load()
	stateSubscriberCRDRetry.Store(int64(retry))
	stateSubscriberCRDSyncTimeout.Store(int64(syncTimeout))
	return func() {
		stateSubscriberCRDRetry.Store(prevRetry)
		stateSubscriberCRDSyncTimeout.Store(prevSync)
	}
}

func newMetaObject(apiVersion, kind, namespace, name string) *metav1.PartialObjectMetadata {
	return &metav1.PartialObjectMetadata{
		TypeMeta: metav1.TypeMeta{APIVersion: apiVersion, Kind: kind},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       namespace,
			Name:            name,
			ResourceVersion: "1",
		},
	}
}

// createMeta writes a PartialObjectMetadata into the fake metadata tracker
// so the corresponding informer's watch fires.
func createMeta(t *testing.T, mc *metadatafake.FakeMetadataClient, gvr schema.GroupVersionResource, obj *metav1.PartialObjectMetadata) {
	t.Helper()
	var target any
	if obj.GetNamespace() == "" {
		target = mc.Resource(gvr)
	} else {
		target = mc.Resource(gvr).Namespace(obj.GetNamespace())
	}
	fakeClient, ok := target.(metadatafake.MetadataClient)
	if !ok {
		t.Fatalf("fake metadata resource client does not implement MetadataClient: %T", target)
	}
	if _, err := fakeClient.CreateFake(obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create %s %s/%s: %v", gvr.Resource, obj.GetNamespace(), obj.GetName(), err)
	}
}

// waitForStateUpdate polls the recording sender for a StateUpdate matching
// kind+name within the deadline.
func waitForStateUpdate(t *testing.T, sender *recordingSender, kind, name string, timeout time.Duration) *protocol.StateUpdatePayload {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, m := range sender.Snapshot() {
			if m.Type != protocol.MsgStateUpdate {
				continue
			}
			var p protocol.StateUpdatePayload
			if err := json.Unmarshal(m.Payload, &p); err != nil {
				continue
			}
			if p.Kind == kind && p.Name == name {
				return &p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func (s *StateSubscriber) hasStore(kind string) bool {
	s.storeMu.RLock()
	defer s.storeMu.RUnlock()
	_, ok := s.stores[kind]
	return ok
}

// TestStateSubscriberMetadataInformerRegistration locks the P4.6 informer
// expansion kinds list: every metadata kind (plus the discover-if-present
// CRDs, which the fake serves as empty lists) registers a store, and a
// metadata-kind change emits a STATE_UPDATE with the right wire labels.
func TestStateSubscriberMetadataInformerRegistration(t *testing.T) {
	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()
	defer setStateSubscriberCRDTunables(50*time.Millisecond, 2*time.Second)()

	client := fake.NewClientset()
	mc := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	sender := &recordingSender{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	subscriber := NewStateSubscriber(client, sender, logger)
	subscriber.SetMetadataClient(mc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	// Typed kinds (Events informer deliberately excluded) + the P4.6
	// metadata expansion set register their stores by ready time.
	expected := []string{
		"Pod", "Service", "Node", "ConfigMap", "Secret",
		"Deployment", "ReplicaSet", "StatefulSet", "DaemonSet",
	}
	for _, k := range metadataInformerKinds {
		expected = append(expected, k.kind)
	}
	for _, kind := range expected {
		if !subscriber.hasStore(kind) {
			t.Errorf("expected informer store for kind %s", kind)
		}
	}

	// The CRD retry loops come online asynchronously (the fake serves all
	// six GVRs, so each syncs on its first attempt).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, k := range crdInformerKinds {
			if !subscriber.hasStore(k.kind) {
				all = false
				break
			}
		}
		if all {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, k := range crdInformerKinds {
		if !subscriber.hasStore(k.kind) {
			t.Errorf("expected CRD informer store for kind %s", k.kind)
		}
	}

	// A change on a metadata-only kind rides the same wire as typed kinds.
	createMeta(t, mc, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		newMetaObject("v1", "Namespace", "", "team-a"))
	found := waitForStateUpdate(t, sender, "Namespace", "team-a", 2*time.Second)
	if found == nil {
		t.Fatal("expected a STATE_UPDATE for Namespace team-a, got none")
	}
	if found.Op != protocol.StateUpdateOpAdded {
		t.Errorf("expected op=added, got %s", found.Op)
	}
	if found.APIGroup != "" || found.APIVersion != "v1" {
		t.Errorf("unexpected wire labels: group=%q version=%q", found.APIGroup, found.APIVersion)
	}
}

// TestStateSubscriberHelmSecretFilter verifies the dedicated Secret
// metadata informer forwards Helm release storage only: the agent-side
// name-prefix guard drops every other Secret even when the transport
// (here: the fake, which ignores field selectors) delivers it.
func TestStateSubscriberHelmSecretFilter(t *testing.T) {
	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()
	defer setStateSubscriberCRDTunables(50*time.Millisecond, 2*time.Second)()

	client := fake.NewClientset()
	mc := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	sender := &recordingSender{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	subscriber := NewStateSubscriber(client, sender, logger)
	subscriber.SetMetadataClient(mc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	secretsGVR := schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	createMeta(t, mc, secretsGVR, newMetaObject("v1", "Secret", "default", "sh.helm.release.v1.myapp.v1"))
	createMeta(t, mc, secretsGVR, newMetaObject("v1", "Secret", "default", "db-credentials"))

	found := waitForStateUpdate(t, sender, "Secret", "sh.helm.release.v1.myapp.v1", 2*time.Second)
	if found == nil {
		t.Fatal("expected a STATE_UPDATE for the Helm release secret, got none")
	}
	// The non-Helm secret must never be forwarded on the metadata path.
	if leaked := waitForStateUpdate(t, sender, "Secret", "db-credentials", 300*time.Millisecond); leaked != nil {
		t.Fatalf("non-Helm secret metadata leaked through the Helm-filtered informer: %+v", leaked)
	}
}

// TestStateSubscriberToleratesAbsentCRD verifies the discover-if-present
// contract: a missing CRD (list errors) never stalls readiness or the
// typed kinds, and the retry loop brings the informer online once the CRD
// is installed.
func TestStateSubscriberToleratesAbsentCRD(t *testing.T) {
	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()
	defer setStateSubscriberCRDTunables(50*time.Millisecond, 500*time.Millisecond)()

	client := fake.NewClientset()
	mc := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())

	// Simulate "velero not installed": every backups list fails until the
	// flag flips.
	var backupsAbsent atomic.Bool
	backupsAbsent.Store(true)
	mc.PrependReactor("list", "backups", func(k8stesting.Action) (bool, runtime.Object, error) {
		if backupsAbsent.Load() {
			return true, nil, errors.New("the server could not find the requested resource")
		}
		return false, nil, nil
	})

	sender := &recordingSender{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	subscriber := NewStateSubscriber(client, sender, logger)
	subscriber.SetMetadataClient(mc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready with an absent CRD")
	}
	if subscriber.hasStore("Backup") {
		t.Fatal("Backup store must not be recorded while the CRD is absent")
	}

	// Metadata kinds keep flowing while the CRD informer retries.
	createMeta(t, mc, schema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		newMetaObject("v1", "Namespace", "", "still-live"))
	if waitForStateUpdate(t, sender, "Namespace", "still-live", 2*time.Second) == nil {
		t.Fatal("expected Namespace events to keep flowing while a CRD is absent")
	}

	// "Install" the CRD: the retry loop's next attempt syncs and records
	// the store.
	backupsAbsent.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !subscriber.hasStore("Backup") {
		time.Sleep(20 * time.Millisecond)
	}
	if !subscriber.hasStore("Backup") {
		t.Fatal("Backup informer did not come online after the CRD appeared")
	}

	createMeta(t, mc, schema.GroupVersionResource{Group: "velero.io", Version: "v1", Resource: "backups"},
		newMetaObject("velero.io/v1", "Backup", "velero", "nightly-2026"))
	found := waitForStateUpdate(t, sender, "Backup", "nightly-2026", 2*time.Second)
	if found == nil {
		t.Fatal("expected a STATE_UPDATE for the late-installed Backup CRD")
	}
	if found.APIGroup != "velero.io" {
		t.Errorf("expected api_group=velero.io, got %q", found.APIGroup)
	}
}

// TestStateSubscriberGatekeeperConstraintDiscovery verifies that resources
// discovered under constraints.gatekeeper.sh get a metadata informer whose
// events are normalized to the stable wire kind "Constraint".
func TestStateSubscriberGatekeeperConstraintDiscovery(t *testing.T) {
	defer setStateSubscriberTunables(50*time.Millisecond, 1*time.Second, 200*time.Millisecond, 24*time.Hour)()
	defer setStateSubscriberCRDTunables(50*time.Millisecond, 2*time.Second)()

	client := fake.NewClientset()
	client.Fake.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: gatekeeperConstraintsGV,
			APIResources: []metav1.APIResource{
				{Name: "k8srequiredlabels", Kind: "K8sRequiredLabels", Namespaced: false},
				{Name: "k8srequiredlabels/status", Kind: "K8sRequiredLabels", Namespaced: false},
			},
		},
	}
	mc := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	sender := &recordingSender{}
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	subscriber := NewStateSubscriber(client, sender, logger)
	subscriber.SetMetadataClient(mc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.Run(ctx)

	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	if !subscriber.WaitReady(readyCtx) {
		t.Fatal("state subscriber did not become ready")
	}

	createMeta(t, mc,
		schema.GroupVersionResource{Group: "constraints.gatekeeper.sh", Version: "v1beta1", Resource: "k8srequiredlabels"},
		newMetaObject("constraints.gatekeeper.sh/v1beta1", "K8sRequiredLabels", "", "require-team-label"))

	found := waitForStateUpdate(t, sender, "Constraint", "require-team-label", 3*time.Second)
	if found == nil {
		t.Fatal("expected a STATE_UPDATE with normalized kind Constraint for the discovered gatekeeper resource")
	}
	if found.APIGroup != "constraints.gatekeeper.sh" {
		t.Errorf("expected api_group=constraints.gatekeeper.sh, got %q", found.APIGroup)
	}
}
