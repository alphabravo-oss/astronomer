package tunnel

import "context"

// NewFakeLocatorForTest builds an in-memory Locator suitable for tests
// outside this package (handler/originator tests need to verify cross-pod
// routing without spinning up redis). The returned Locator's Lookup
// consults `entries` directly; Set/Delete/Drain/refreshLoop are no-ops
// because rdb stays nil.
//
// Exported because cross-package tests in internal/handler need it.
// Production code paths never invoke this constructor.
func NewFakeLocatorForTest(selfAddress string, entries map[string]string) *Locator {
	cp := make(map[string]string, len(entries))
	for k, v := range entries {
		cp[k] = v
	}
	return &Locator{
		address: selfAddress,
		cancels: map[string]context.CancelFunc{},
		static:  cp,
	}
}
