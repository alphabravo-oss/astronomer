package agent

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"
)

// DiscoveryRevision invalidates delivery's slower refresh when a relevant CRD
// changes. Denied metadata access does not broaden RBAC or certify discovery;
// bounded periodic, context-aware API discovery remains the authority.
func (s *StateSubscriber) DiscoveryRevision() uint64 { return s.discoveryRevision.Load() }
func (s *StateSubscriber) registerDiscoveryRevision(factory metadatainformer.SharedInformerFactory) {
	informer := factory.ForResource(schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}).Informer()
	changed := func(object any) {
		meta, ok := metaFromObj(object)
		if !ok {
			return
		}
		for _, group := range []string{"source.toolkit.fluxcd.io", "kustomize.toolkit.fluxcd.io", "helm.toolkit.fluxcd.io", "longhorn.io", "cert-manager.io", "gateway.networking.k8s.io", "snapshot.storage.k8s.io"} {
			if strings.HasSuffix(meta.GetName(), "."+group) {
				s.discoveryRevision.Add(1)
				return
			}
		}
	}
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: changed, DeleteFunc: changed, UpdateFunc: func(old, new any) {
		a, aOK := metaFromObj(old)
		b, bOK := metaFromObj(new)
		if aOK && bOK && a.GetResourceVersion() != b.GetResourceVersion() {
			changed(new)
		}
	}})
}
