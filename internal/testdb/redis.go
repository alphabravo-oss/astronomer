//go:build integration

package testdb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

// RedisImage matches the dev compose stack.
const RedisImage = "redis:8.10.2-alpine"

// Redis starts a dedicated Redis container for the test and returns its URL
// (redis://host:port). The container is terminated when the test finishes.
func Redis(t testing.TB) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c, err := redis.Run(ctx, RedisImage)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("testdb: start %s: %v", RedisImage, err)
	}
	url, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("testdb: redis connection string: %v", err)
	}
	return url
}

// RedisAddr is Redis without the scheme (host:port).
func RedisAddr(t testing.TB) string {
	t.Helper()
	return strings.TrimPrefix(Redis(t), "redis://")
}
