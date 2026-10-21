package middleware

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestBindingsColdBurstLoadsOnce(t *testing.T) {
	t.Parallel()
	fake := newFakeUserBindingsQuerier()
	user := uuid.NewString()
	fake.setRows(user, nil)
	querier := NewSQLCRBACQuerierWithCache(fake, NewRBACCache())
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			<-start
			if _, err := querier.GetUserBindings(t.Context(), user); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if n := fake.listCount(); n != 1 {
		t.Fatalf("cold burst issued %d binding queries, want 1", n)
	}
}

func TestInvalidationRejectsInflightBindingCacheFill(t *testing.T) {
	t.Parallel()
	cache := NewRBACCache()
	generation := cache.generation.Load()
	cache.InvalidateRBACUserLocal("user")
	cache.Put("user", nil, generation)
	if _, ok := cache.Get("user"); ok {
		t.Fatal("pre-revocation query repopulated the cache")
	}
}
