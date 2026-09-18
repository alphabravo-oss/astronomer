package agent

import (
	"context"
	"time"

	"k8s.io/client-go/metadata/metadatainformer"
)

// runCRDInformer loops trying to bring up a metadata informer for a
// discover-if-present CRD. Discovery is checked first so an absent CRD does not
// create a reflector that emits an error on every list retry.
func (s *StateSubscriber) runCRDInformer(ctx context.Context, k metadataKind, parentStop <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		default:
		}
		if !s.isCRDResourceAvailable(k) {
			s.log.Debug("state subscriber: CRD not discovered, will retry",
				"gvr", k.gvr.String(), "retry_in", getStateSubscriberCRDRetry().String())
			select {
			case <-ctx.Done():
				return
			case <-parentStop:
				return
			case <-time.After(getStateSubscriberCRDRetry()):
			}
			continue
		}

		attempt := metadatainformer.NewSharedInformerFactory(s.meta, getStateSubscriberResyncPeriod())
		inf := attempt.ForResource(k.gvr).Informer()
		_, _ = inf.AddEventHandler(s.handlers(k.kind, k.apiGroup, k.apiVersion))

		// The watcher exclusively owns innerStop so failed attempts cannot leak
		// goroutines or race shutdown with a second close.
		innerStop := make(chan struct{})
		iterDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
			case <-parentStop:
			case <-iterDone:
			}
			close(innerStop)
		}()
		attempt.Start(innerStop)

		syncStop := make(chan struct{})
		go func() {
			select {
			case <-innerStop:
			case <-time.After(getStateSubscriberCRDSyncTimeout()):
			}
			close(syncStop)
		}()
		ok := false
		for _, synced := range attempt.WaitForCacheSync(syncStop) {
			ok = synced
			break
		}
		if ok {
			s.log.Info("state subscriber: CRD informer online", "gvr", k.gvr.String(), "kind", k.kind)
			s.recordStore(k.kind, k.apiGroup, k.apiVersion, inf.GetStore(), inf.HasSynced)
			<-innerStop
			return
		}

		close(iterDone)
		s.log.Debug("state subscriber: CRD not yet available, will retry",
			"gvr", k.gvr.String(), "retry_in", getStateSubscriberCRDRetry().String())
		select {
		case <-ctx.Done():
			return
		case <-parentStop:
			return
		case <-time.After(getStateSubscriberCRDRetry()):
		}
	}
}

func (s *StateSubscriber) isCRDResourceAvailable(k metadataKind) bool {
	if s.crdAvailable != nil {
		return s.crdAvailable(k)
	}
	resources, err := s.client.Discovery().ServerResourcesForGroupVersion(k.apiGroup + "/" + k.apiVersion)
	if err != nil || resources == nil {
		return false
	}
	for _, resource := range resources.APIResources {
		if resource.Name == k.gvr.Resource {
			return true
		}
	}
	return false
}
