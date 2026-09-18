package rbac

import "context"

// BindingQuerier is the domain-owned read contract used by authorization
// middleware and handlers. Keeping the contract in rbac prevents HTTP domain
// code from depending on router composition merely to describe authorization.
type BindingQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]RoleBinding, error)
}

// CacheInvalidator is implemented by cached binding stores. Mutations use it
// to make revocations visible immediately rather than waiting for a TTL.
type CacheInvalidator interface {
	Invalidate(userID string)
	InvalidateAll()
}

// CacheStatus lets startup wiring prove that an invalidator has an actual
// cache behind it without coupling domain handlers to a cache implementation.
type CacheStatus interface {
	RBACCacheEnabled() bool
}
