package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type sharedRevocations struct {
	mu      sync.Mutex
	revoked map[string]bool
	cutoffs map[uuid.UUID]time.Time
	reads   int
}

func newSharedRevocations() *sharedRevocations {
	return &sharedRevocations{revoked: map[string]bool{}, cutoffs: map[uuid.UUID]time.Time{}}
}
func (s *sharedRevocations) IsJWTRevoked(_ context.Context, jti string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return s.revoked[jti], nil
}
func (s *sharedRevocations) UserTokensInvalidatedAt(_ context.Context, userID uuid.UUID) (time.Time, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	cutoff, ok := s.cutoffs[userID]
	return cutoff, ok, nil
}

func waitSecurity(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("security cache condition not met before timeout")
}

func TestDistributedJWTAndRBACInvalidationAcrossReplicas(t *testing.T) {
	mini := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })

	revocations := newSharedRevocations()
	jwtA := auth.NewJWTManager("distributed-cache-test-secret", 60)
	jwtB := auth.NewJWTManager("distributed-cache-test-secret", 60)
	jwtA.SetRevocationChecker(revocations)
	jwtB.SetRevocationChecker(revocations)
	rbacA := appmiddleware.NewRBACCacheWithOptions(time.Minute, 10)
	rbacB := appmiddleware.NewRBACCacheWithOptions(time.Minute, 10)
	targetA := securityCacheTarget{jwt: jwtA, rbac: rbacA}
	targetB := securityCacheTarget{jwt: jwtB, rbac: rbacB}
	coordA := cacheinvalidate.New(clientA, targetA, "pod-a", 50*time.Millisecond, nil)
	coordB := cacheinvalidate.New(clientB, targetB, "pod-b", 50*time.Millisecond, nil)
	jwtA.SetCacheInvalidationCoordinator(coordA)
	jwtB.SetCacheInvalidationCoordinator(coordB)
	rbacA.SetInvalidationCoordinator(coordA)
	rbacB.SetInvalidationCoordinator(coordB)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordA.Run(ctx)
	go coordB.Run(ctx)
	waitSecurity(t, time.Second, func() bool { return coordA.Healthy() && coordB.Healthy() })

	userID := uuid.New()
	token, err := jwtA.GenerateAccessToken(userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jwtA.ValidateToken(token); err != nil {
		t.Fatal(err)
	}
	if _, err := jwtB.ValidateToken(token); err != nil {
		t.Fatal(err)
	}
	revocations.mu.Lock()
	revocations.cutoffs[userID] = time.Now().Add(time.Second)
	revocations.mu.Unlock()
	jwtA.InvalidateUser(ctx, userID)
	waitSecurity(t, time.Second, func() bool { _, err := jwtB.ValidateToken(token); return err != nil })

	jtiUser := uuid.New()
	jtiToken, _ := jwtA.GenerateAccessToken(jtiUser)
	jtiClaims, err := jwtA.ValidateToken(jtiToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jwtB.ValidateToken(jtiToken); err != nil {
		t.Fatal(err)
	}
	revocations.mu.Lock()
	revocations.revoked[jtiClaims.ID] = true
	revocations.mu.Unlock()
	jwtA.InvalidateJTI(ctx, jtiClaims.ID)
	waitSecurity(t, time.Second, func() bool { _, err := jwtB.ValidateToken(jtiToken); return err != nil })

	binding := []rbac.RoleBinding{{UserID: userID.String()}}
	rbacA.Put(userID.String(), binding)
	rbacB.Put(userID.String(), binding)
	rbacA.Invalidate(userID.String())
	waitSecurity(t, time.Second, func() bool { _, ok := rbacB.Get(userID.String()); return !ok })

	otherID := uuid.New().String()
	rbacA.Put(otherID, binding)
	rbacB.Put(otherID, binding)
	rbacA.InvalidateAll()
	waitSecurity(t, time.Second, func() bool { return rbacB.Len() == 0 })
}

func TestRedisDisconnectBypassesPrimedJWTAndRBACCaches(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr(), DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	revocations := newSharedRevocations()
	manager := auth.NewJWTManager("disconnect-test-secret", 60)
	manager.SetRevocationChecker(revocations)
	cache := appmiddleware.NewRBACCacheWithOptions(time.Minute, 10)
	target := securityCacheTarget{jwt: manager, rbac: cache}
	coord := cacheinvalidate.New(client, target, "pod", 20*time.Millisecond, nil)
	manager.SetCacheInvalidationCoordinator(coord)
	cache.SetInvalidationCoordinator(coord)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coord.Run(ctx)
	waitSecurity(t, time.Second, coord.Healthy)

	userID := uuid.New()
	token, _ := manager.GenerateAccessToken(userID)
	if _, err := manager.ValidateToken(token); err != nil {
		t.Fatal(err)
	}
	cache.Put(userID.String(), []rbac.RoleBinding{{UserID: userID.String()}})
	revocations.mu.Lock()
	revocations.cutoffs[userID] = time.Now().Add(time.Second)
	revocations.mu.Unlock()
	mini.Close()
	waitSecurity(t, 2*time.Second, func() bool { return !coord.Healthy() })
	if _, err := manager.ValidateToken(token); err == nil {
		t.Fatal("unhealthy coordinator trusted a positive JWT cache hit")
	}
	if _, ok := cache.Get(userID.String()); ok {
		t.Fatal("unhealthy coordinator trusted a positive RBAC cache hit")
	}
}
