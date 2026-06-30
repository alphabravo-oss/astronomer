// Cross-pod agent locator.
//
// The Hub is in-memory: a given cluster's agent WebSocket terminates on
// exactly one server pod, and only that pod's Hub can route messages to
// it. With more than one server replica, an inbound request (kubectl
// shell, /api/v1/clusters/{id}/k8s/*, image-scan list, etc.) can land
// on a sibling replica that doesn't hold the WS, and the local Hub
// 503s. Worse, in practice nginx upstream keep-alive pins all requests
// to the same pod, so the failure rate is ~100% not 1/N.
//
// The locator solves this with a redis-backed cluster_id → pod_address
// directory. Each pod's Hub writes its own address into the directory
// on agent connect (with a TTL) and clears it on disconnect / shutdown.
// The Proxy handler reads from the directory whenever its local Hub
// reports the agent missing, then reverse-proxies to the address it
// finds. The agent's pod always wins the race because nobody else
// writes for that cluster_id.
//
// The TTL exists so a pod that crashes without removing its keys
// doesn't leave stale entries forever — the agent re-establishes a WS
// to a sibling within seconds and that sibling overwrites the entry.
//
// Storage keys are namespaced `tunnel:agent:<cluster_id>` to avoid
// colliding with the asynq queue prefixes that share the same redis.

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// locatorKeyPrefix is the redis key prefix. Each cluster's address is
// stored at `tunnel:agent:<cluster_id>`. SET on connect with TTL;
// DEL on disconnect.
const locatorKeyPrefix = "tunnel:agent:"

// locatorEntryTTL is how long each cluster_id→address entry survives
// without a refresh. The refresh loop ticks at ~half this value so a
// single missed tick doesn't expire a healthy entry.
const locatorEntryTTL = 60 * time.Second

// locatorRefreshInterval is how often the per-cluster ticker re-runs
// SET to extend the TTL. Half the TTL gives one missed-tick tolerance.
const locatorRefreshInterval = 25 * time.Second

// Locator publishes / looks up cluster-id → pod-address mappings in
// redis. Optional on the Hub: when nil (single-replica deployments,
// unit tests) the proxy falls back to the in-memory check only.
type Locator struct {
	rdb     *redis.Client
	address string // this pod's reachable address (e.g. "10.42.0.7:8000")
	log     *slog.Logger

	mu      sync.Mutex
	cancels map[string]context.CancelFunc // cluster_id → refresh-loop cancel
	// static is set ONLY by NewFakeLocatorForTest; Lookup checks it
	// before consulting redis so unit tests don't need miniredis.
	static map[string]string
}

// NewLocatorFromAsynqRedisURL builds a Locator using the same redis
// connection-string the rest of the platform uses (asynq parses it).
// The address argument is what siblings will dial when reverse-proxying
// to this pod — typically `<pod-ip>:8000`. Empty address disables
// publish (read-only mode for tests / one-replica installs).
func NewLocatorFromAsynqRedisURL(redisURL, address string, log *slog.Logger) (*Locator, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url for tunnel locator: %w", err)
	}
	cfg, ok := opt.MakeRedisClient().(*redis.Client)
	if !ok {
		return nil, errors.New("tunnel locator: asynq redis backend is not a single-node go-redis client (cluster mode not supported)")
	}
	return &Locator{
		rdb:     cfg,
		address: address,
		log:     log,
		cancels: map[string]context.CancelFunc{},
	}, nil
}

// Address returns the pod-local address that this locator publishes.
// Siblings comparing this to their own address can detect "the agent
// is local, no need to reverse-proxy" without an extra redis hop.
func (l *Locator) Address() string {
	if l == nil {
		return ""
	}
	return l.address
}

// Set publishes (and keeps refreshing) the cluster_id → pod address
// mapping. Idempotent: a repeat call for the same cluster_id replaces
// the previous refresh loop. nil-receiver and empty-address Set are no-ops.
func (l *Locator) Set(parent context.Context, clusterID string) {
	if l == nil || l.address == "" || clusterID == "" {
		return
	}
	l.mu.Lock()
	if cancel, exists := l.cancels[clusterID]; exists {
		cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	l.cancels[clusterID] = cancel
	l.mu.Unlock()

	// Immediate set so the address is visible before the first tick.
	if err := l.write(ctx, clusterID); err != nil && l.log != nil {
		l.log.Warn("tunnel locator: initial set failed",
			slog.String("cluster_id", clusterID), slog.String("error", err.Error()))
	}

	go l.refreshLoop(ctx, clusterID)
}

// Delete clears the mapping immediately and stops the refresh loop.
// nil-receiver safe.
func (l *Locator) Delete(ctx context.Context, clusterID string) {
	if l == nil || clusterID == "" {
		return
	}
	l.mu.Lock()
	if cancel, exists := l.cancels[clusterID]; exists {
		cancel()
		delete(l.cancels, clusterID)
	}
	l.mu.Unlock()

	if l.rdb == nil {
		return
	}
	// CAS delete (M10): only remove the entry if it still points at THIS pod.
	// On a fast agent move A→B, B overwrites the entry with B's address; pod A's
	// stale disconnect/refresh-stop must NOT clobber B's fresh entry (which would
	// leave the cluster connected-but-undirectoried, 503-ing on non-owning
	// replicas until the TTL refresh). Mirrors the in-memory owner-checked
	// DeleteIfSame. Empty address (no own identity) can't own anything → skip.
	if l.address == "" {
		return
	}
	delCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := locatorCASDelete.Run(delCtx, l.rdb, []string{locatorKeyPrefix + clusterID}, l.address).Err(); err != nil && err != redis.Nil && l.log != nil {
		l.log.Warn("tunnel locator: CAS delete failed",
			slog.String("cluster_id", clusterID), slog.String("error", err.Error()))
	}
}

// locatorCASDelete removes the key ONLY when its value still equals this pod's
// address (ARGV[1]) — a compare-and-delete so a moved agent's stale owner can't
// clobber the new owner's entry. Returns 1 if deleted, 0 if the value differed.
var locatorCASDelete = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
`)

// Lookup returns the address (e.g. "10.42.0.7:8000") of the pod
// currently owning the cluster_id's WebSocket. Empty string + nil err
// when no entry exists (agent isn't connected anywhere).
func (l *Locator) Lookup(ctx context.Context, clusterID string) (string, error) {
	if l == nil || clusterID == "" {
		return "", nil
	}
	// Test-only fake path: when the locator was built with
	// NewFakeLocatorForTest the in-memory `static` map drives lookups
	// without any redis at all. Production paths leave `static` nil.
	if l.static != nil {
		l.mu.Lock()
		addr := l.static[clusterID]
		l.mu.Unlock()
		return addr, nil
	}
	if l.rdb == nil {
		return "", nil
	}
	getCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addr, err := l.rdb.Get(getCtx, locatorKeyPrefix+clusterID).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return addr, err
}

// Drain cancels all per-cluster refresh loops and deletes every key
// this pod owns. Called on Hub.Drain so siblings see the agent vanish
// immediately instead of waiting out the TTL.
func (l *Locator) Drain(ctx context.Context) {
	if l == nil {
		return
	}
	l.mu.Lock()
	ids := make([]string, 0, len(l.cancels))
	for id, cancel := range l.cancels {
		cancel()
		ids = append(ids, id)
	}
	l.cancels = map[string]context.CancelFunc{}
	l.mu.Unlock()
	for _, id := range ids {
		l.Delete(ctx, id)
	}
}

func (l *Locator) write(ctx context.Context, clusterID string) error {
	if l.rdb == nil {
		return nil
	}
	setCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return l.rdb.Set(setCtx, locatorKeyPrefix+clusterID, l.address, locatorEntryTTL).Err()
}

func (l *Locator) refreshLoop(ctx context.Context, clusterID string) {
	ticker := time.NewTicker(locatorRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.write(ctx, clusterID); err != nil && l.log != nil {
				l.log.Warn("tunnel locator: refresh failed",
					slog.String("cluster_id", clusterID), slog.String("error", err.Error()))
			}
		}
	}
}
