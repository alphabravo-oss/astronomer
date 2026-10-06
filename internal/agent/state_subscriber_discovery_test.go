package agent

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	metadatafake "k8s.io/client-go/metadata/fake"
	"k8s.io/client-go/metadata/metadatainformer"
)

func TestDiscoveryRevisionTracksRelevantMetadataOnlyChanges(t *testing.T) {
	client := metadatafake.NewSimpleMetadataClient(metadatafake.NewTestScheme())
	factory := metadatainformer.NewSharedInformerFactory(client, 0)
	subscriber := NewStateSubscriber(nil, nil, nil)
	subscriber.registerDiscoveryRevision(factory)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); factory.Shutdown() }()
	factory.Start(ctx.Done())
	syncCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	for _, ok := range factory.WaitForCacheSync(syncCtx.Done()) {
		if !ok {
			t.Fatal("CRD metadata cache unsynced")
		}
	}
	gvr := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	obj := newMetaObject("apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "certificates.cert-manager.io")
	obj.ResourceVersion = "1"
	createMeta(t, client, gvr, obj)
	await := func(expected uint64) {
		t.Helper()
		if err := wait.PollUntilContextTimeout(ctx, 5*time.Millisecond, 5*time.Second, true, func(context.Context) (bool, error) { return subscriber.DiscoveryRevision() == expected, nil }); err != nil {
			t.Fatal("metadata revision did not advance", expected, err)
		}
	}
	await(1)
	obj.ResourceVersion = "2"
	if _, err := client.Resource(gvr).(interface {
		UpdateFake(*metav1.PartialObjectMetadata, metav1.UpdateOptions, ...string) (*metav1.PartialObjectMetadata, error)
	}).UpdateFake(obj, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	await(2)
	if err := client.Resource(gvr).Delete(ctx, obj.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	await(3)
}
