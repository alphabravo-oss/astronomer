package server

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

// nativeAuthorizer is the seam the k8s-proxy authz hook uses to consult native
// per-CRD rules AFTER the coarse check has denied. Allow returns true only
// when an explicitly-authored native rule grants the request; a nil authorizer
// (feature off) means the hook keeps the coarse deny unchanged.
type nativeAuthorizer interface {
	Allow(ctx context.Context, userID, clusterID, namespace, apiGroup, resource, verb string) bool
}

// nativeRuleQuerier is the narrow DB surface the authorizer needs.
type nativeRuleQuerier interface {
	ListNativeRBACRulesByUser(ctx context.Context, userID uuid.UUID) ([]sqlc.NativeRbacRule, error)
}

// nativeRBACAuthorizer loads a user's native rules (short-TTL cached, since the
// hook only fires on coarse-deny) and evaluates them with rbac.NativeAllow.
type nativeRBACAuthorizer struct {
	q   nativeRuleQuerier
	ttl time.Duration

	mu    sync.Mutex
	cache map[string]nativeCacheEntry
}

type nativeCacheEntry struct {
	rules []rbac.NativeRule
	exp   time.Time
}

const nativeRBACCacheCap = 2000

func newNativeRBACAuthorizer(q nativeRuleQuerier) *nativeRBACAuthorizer {
	return &nativeRBACAuthorizer{q: q, ttl: 15 * time.Second, cache: map[string]nativeCacheEntry{}}
}

// Invalidate drops a user's cached rules after an authoring change so a new
// grant/revoke takes effect immediately rather than after the TTL.
func (a *nativeRBACAuthorizer) Invalidate(userID string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	delete(a.cache, userID)
	a.mu.Unlock()
}

func (a *nativeRBACAuthorizer) Allow(ctx context.Context, userID, clusterID, namespace, apiGroup, resource, verb string) bool {
	if a == nil || userID == "" {
		return false
	}
	rules, ok := a.load(ctx, userID)
	if !ok || len(rules) == 0 {
		return false
	}
	return rbac.NativeAllow(rules, clusterID, namespace, apiGroup, resource, verb)
}

func (a *nativeRBACAuthorizer) load(ctx context.Context, userID string) ([]rbac.NativeRule, bool) {
	now := time.Now()
	a.mu.Lock()
	if e, ok := a.cache[userID]; ok && now.Before(e.exp) {
		a.mu.Unlock()
		return e.rules, true
	}
	a.mu.Unlock()

	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, false
	}
	rows, err := a.q.ListNativeRBACRulesByUser(ctx, uid)
	if err != nil {
		// DB error → fail closed (no native grant); the coarse deny stands.
		return nil, false
	}
	rules := make([]rbac.NativeRule, 0, len(rows))
	for _, r := range rows {
		nr := rbac.NativeRule{
			Namespace: r.Namespace,
			APIGroup:  r.ApiGroup,
			Resource:  r.Resource,
			Verbs:     r.Verbs,
		}
		if r.ClusterID.Valid {
			nr.ClusterID = uuid.UUID(r.ClusterID.Bytes).String()
		}
		rules = append(rules, nr)
	}

	a.mu.Lock()
	// Crude bound: users are not attacker-multipliable, but clear on overflow
	// so the map can't grow without limit over a long-lived process.
	// ponytail: clear-on-overflow; swap for an LRU if this ever churns hot.
	if len(a.cache) >= nativeRBACCacheCap {
		a.cache = map[string]nativeCacheEntry{}
	}
	a.cache[userID] = nativeCacheEntry{rules: rules, exp: now.Add(a.ttl)}
	a.mu.Unlock()
	return rules, true
}
