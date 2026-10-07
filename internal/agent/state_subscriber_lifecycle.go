package agent

import (
	"context"
	"fmt"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata/metadatainformer"
)

// waitForInitialCaches gives all built-in informers one bounded bootstrap
// window. A denied kind remains unavailable through its own HasSynced check;
// healthy kinds may still publish events after the window. Shutdown cancels
// both waits immediately, rather than depending on a channel closed by Run's
// deferred cleanup (which cannot execute while Run is waiting here).
func (s *StateSubscriber) waitForInitialCaches(ctx context.Context, typed informers.SharedInformerFactory, metadata metadatainformer.SharedInformerFactory) bool {
	syncCtx, cancel := context.WithTimeout(ctx, s.syncTimeout)
	defer cancel()
	for kind, synced := range typed.WaitForCacheSync(syncCtx.Done()) {
		if !synced {
			s.log.Warn("state subscriber: typed cache failed to sync (RBAC?)", "type", fmt.Sprint(kind))
		}
	}
	if metadata != nil {
		for kind, synced := range metadata.WaitForCacheSync(syncCtx.Done()) {
			if !synced {
				s.log.Warn("state subscriber: metadata cache failed to sync (RBAC?)", "type", kind.String())
			}
		}
	}
	return ctx.Err() == nil
}
