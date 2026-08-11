package charlie

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

type runtimeRedisFake struct {
	info string
	err  error
}

func (f runtimeRedisFake) Ping(ctx context.Context) *redis.StatusCmd {
	command := redis.NewStatusCmd(ctx)
	if f.err != nil {
		command.SetErr(f.err)
	} else {
		command.SetVal("PONG")
	}
	return command
}

func (f runtimeRedisFake) Info(ctx context.Context, _ ...string) *redis.StringCmd {
	command := redis.NewStringCmd(ctx)
	if f.err != nil {
		command.SetErr(f.err)
	} else {
		command.SetVal(f.info)
	}
	return command
}

func TestRuntimeRedisUsesFixedSafeInfoAllowlist(t *testing.T) {
	adapter := &RuntimeCapabilityAdapter{config: RuntimeCapabilityConfig{Redis: runtimeRedisFake{info: strings.Join([]string{
		"redis_version:7.4.1", "connected_clients:3", "used_memory:4096", "maxmemory_policy:allkeys-lru",
		"requirepass:SENTINEL", "master_auth:SENTINEL", "unknown_field:SENTINEL",
	}, "\n")}}}
	result, err := adapter.redisHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := marshalBounded(result, 64<<10)
	text := string(encoded)
	if strings.Contains(text, "SENTINEL") || !strings.Contains(text, `"connected_clients":3`) || !strings.Contains(text, `"maxmemory_policy":"allkeys-lru"`) {
		t.Fatalf("unsafe or incomplete Redis projection: %s", encoded)
	}
}

func TestRuntimeKeyStatusReturnsCountsAndSentinelNamesOnly(t *testing.T) {
	adapter := &RuntimeCapabilityAdapter{config: RuntimeCapabilityConfig{
		EncryptionKeyCount: 2,
		JWTKeyCount:        1,
		InsecureDevKeys:    []string{"secret_key"},
	}}
	result := adapter.keyStatus()
	encoded, _ := marshalBounded(result, 64<<10)
	text := string(encoded)
	if !strings.Contains(text, `"encryption_rotation_in_progress":true`) ||
		!strings.Contains(text, `"jwt_rotation_in_progress":false`) ||
		!strings.Contains(text, `"secret_key"`) {
		t.Fatalf("incomplete key status: %s", encoded)
	}
	for _, forbidden := range []string{"fernet", "jwt-signing-value", "private-key-value"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("key status leaked material %q: %s", forbidden, encoded)
		}
	}
}
