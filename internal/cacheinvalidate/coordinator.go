// Package cacheinvalidate coordinates security-cache invalidation between
// server replicas. Messages contain identifiers only; credentials, JWTs,
// cookies, role rules, and other secret material are never serialized.
package cacheinvalidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

const (
	Channel       = "astronomer:security-cache-invalidation:v1"
	JWTEpochKey   = "astronomer:security-cache-invalidation:jwt:epoch"
	RBACEpochKey  = "astronomer:security-cache-invalidation:rbac:epoch"
	WireVersion   = 1
	DefaultPeriod = 500 * time.Millisecond
)

// Kind identifies the narrow local invalidation to apply.
type Kind string

const (
	KindJWTJTI   Kind = "jwt_jti"
	KindJWTUser  Kind = "jwt_user"
	KindRBACUser Kind = "rbac_user"
	KindRBACAll  Kind = "rbac_all"
)

// Message is the stable, secret-free Redis wire contract.
type Message struct {
	Version   int       `json:"version"`
	Kind      Kind      `json:"kind"`
	SubjectID string    `json:"subject_id,omitempty"`
	Epoch     int64     `json:"epoch"`
	Origin    string    `json:"origin"`
	Time      time.Time `json:"time"`
}

// LocalTarget applies invalidation without rebroadcasting it.
type LocalTarget interface {
	InvalidateJWTJTILocal(jti string)
	InvalidateJWTUserLocal(userID string)
	InvalidateJWTAllLocal()
	InvalidateRBACUserLocal(userID string)
	InvalidateRBACAllLocal()
}

// Broadcaster is the cache-facing coordinator surface.
type Broadcaster interface {
	Healthy() bool
	Broadcast(context.Context, Kind, string) error
}

// Coordinator owns Redis subscription, epoch reconciliation, and health.
type Coordinator struct {
	rdb    redis.UniversalClient
	target LocalTarget
	origin string
	period time.Duration
	log    *slog.Logger

	healthy atomic.Bool
	started atomic.Bool
	lastLog atomic.Int64
	mu      sync.Mutex
	epochs  map[string]int64
}

var (
	metricsOnce  sync.Once
	publishTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "cache_invalidation_publish_total", Help: "Security cache invalidation publish attempts."},
		[]string{"kind", "result"},
	)
	receiveTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "cache_invalidation_receive_total", Help: "Security cache invalidation messages received."},
		[]string{"kind", "result"},
	)
	epochGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cache_invalidation_epoch", Help: "Last observed security cache invalidation epoch."},
		[]string{"cache"},
	)
	lagGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "cache_invalidation_lag_seconds", Help: "Age of the last received invalidation message."},
		[]string{"cache"},
	)
	bypassTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "security_cache_bypass_total", Help: "Positive security-cache hits bypassed while distributed state is unhealthy."},
		[]string{"cache", "reason"},
	)
	healthGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{Name: "cache_invalidation_subscriber_healthy", Help: "Whether distributed security cache invalidation is healthy."},
	)
)

func registerInvalidationMetrics() {
	metricsOnce.Do(func() {
		for _, collector := range []prometheus.Collector{publishTotal, receiveTotal, epochGauge, lagGauge, bypassTotal, healthGauge} {
			if err := prometheus.Register(collector); err != nil {
				if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
					panic(err)
				}
			}
		}
	})
}

func New(rdb redis.UniversalClient, target LocalTarget, origin string, period time.Duration, log *slog.Logger) *Coordinator {
	registerInvalidationMetrics()
	if origin == "" {
		origin, _ = os.Hostname()
	}
	if origin == "" {
		origin = "unknown"
	}
	if period <= 0 {
		period = DefaultPeriod
	}
	if log == nil {
		log = slog.Default()
	}
	return &Coordinator{rdb: rdb, target: target, origin: origin, period: period, log: log, epochs: map[string]int64{"jwt": 0, "rbac": 0}}
}

// NewLocalOnly explicitly enables process-local cache use. It is appropriate
// only for a single-replica development deployment and must be accompanied by
// an operator-visible warning at the wiring layer.
func NewLocalOnly(target LocalTarget, origin string, log *slog.Logger) *Coordinator {
	c := New(nil, target, origin, DefaultPeriod, log)
	c.started.Store(true)
	c.setHealthy(true)
	return c
}

func (c *Coordinator) Healthy() bool { return c != nil && c.healthy.Load() }
func (c *Coordinator) Started() bool { return c != nil && c.started.Load() }
func RecordBypass(cache, reason string) {
	registerInvalidationMetrics()
	bypassTotal.WithLabelValues(cache, reason).Inc()
}

// Broadcast runs after the caller has invalidated its local cache. Epoch
// increment precedes publish so a lost Pub/Sub message is recovered by polling.
func (c *Coordinator) Broadcast(ctx context.Context, kind Kind, subjectID string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	domain, err := domainFor(kind)
	if err != nil {
		return err
	}
	if kind != KindRBACAll && subjectID == "" {
		return fmt.Errorf("subject_id is required for invalidation kind %q", kind)
	}
	epoch, err := c.rdb.Incr(ctx, epochKey(domain)).Result()
	if err != nil {
		c.markUnhealthy("epoch increment failed", err)
		publishTotal.WithLabelValues(string(kind), "error").Inc()
		return fmt.Errorf("increment %s invalidation epoch: %w", domain, err)
	}
	msg := Message{Version: WireVersion, Kind: kind, SubjectID: subjectID, Epoch: epoch, Origin: c.origin, Time: time.Now().UTC()}
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := c.rdb.Publish(ctx, Channel, payload).Err(); err != nil {
		c.markUnhealthy("publish failed", err)
		publishTotal.WithLabelValues(string(kind), "error").Inc()
		return fmt.Errorf("publish invalidation: %w", err)
	}
	c.setEpoch(domain, epoch)
	publishTotal.WithLabelValues(string(kind), "ok").Inc()
	return nil
}

func (c *Coordinator) Run(ctx context.Context) {
	if c == nil || c.rdb == nil {
		return
	}
	c.started.Store(true)
	for ctx.Err() == nil {
		pubsub := c.rdb.Subscribe(ctx, Channel)
		if _, err := pubsub.Receive(ctx); err != nil {
			c.markUnhealthy("subscribe failed", err)
			_ = pubsub.Close()
			c.wait(ctx)
			continue
		}
		wasHealthy := c.Healthy()
		if err := c.reconcile(ctx, !wasHealthy); err != nil {
			c.markUnhealthy("initial epoch reconciliation failed", err)
			_ = pubsub.Close()
			c.wait(ctx)
			continue
		}
		c.setHealthy(true)
		messages := pubsub.Channel(redis.WithChannelHealthCheckInterval(c.period))
		ticker := time.NewTicker(c.period)
		connected := true
		for connected {
			select {
			case <-ctx.Done():
				connected = false
			case msg, ok := <-messages:
				if !ok {
					c.markUnhealthy("subscriber disconnected", errors.New("channel closed"))
					connected = false
					break
				}
				c.handlePayload([]byte(msg.Payload))
			case <-ticker.C:
				if err := c.reconcile(ctx, false); err != nil {
					c.markUnhealthy("epoch reconciliation failed", err)
					connected = false
				}
			}
		}
		ticker.Stop()
		_ = pubsub.Close()
		c.wait(ctx)
	}
}

func (c *Coordinator) wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(c.period):
	}
}

func (c *Coordinator) reconcile(ctx context.Context, reconnect bool) error {
	values, err := c.rdb.MGet(ctx, JWTEpochKey, RBACEpochKey).Result()
	if err != nil {
		return err
	}
	remote := map[string]int64{"jwt": redisInt(values[0]), "rbac": redisInt(values[1])}
	if reconnect {
		// Invalidate before re-enabling cache hits even when epochs are equal: a
		// disconnect may have overlapped an unobservable mutation.
		c.invalidateJWTAll()
		c.invalidateRBACAll()
	}
	for _, domain := range []string{"jwt", "rbac"} {
		if remote[domain] > c.epoch(domain) {
			if domain == "jwt" {
				c.invalidateJWTAll()
			} else {
				c.invalidateRBACAll()
			}
			c.setEpoch(domain, remote[domain])
		}
	}
	return nil
}

func (c *Coordinator) handlePayload(payload []byte) {
	var msg Message
	if err := json.Unmarshal(payload, &msg); err != nil || msg.Version != WireVersion || msg.Epoch <= 0 {
		receiveTotal.WithLabelValues("unknown", "invalid").Inc()
		return
	}
	domain, err := domainFor(msg.Kind)
	if err != nil {
		receiveTotal.WithLabelValues(string(msg.Kind), "invalid").Inc()
		return
	}
	if msg.Kind != KindRBACAll && msg.SubjectID == "" {
		receiveTotal.WithLabelValues(string(msg.Kind), "invalid").Inc()
		return
	}
	current := c.epoch(domain)
	if msg.Epoch <= current {
		receiveTotal.WithLabelValues(string(msg.Kind), "duplicate").Inc()
		return
	}
	if msg.Epoch > current+1 {
		if domain == "jwt" {
			c.invalidateJWTAll()
		} else {
			c.invalidateRBACAll()
		}
	} else {
		if c.target == nil {
			return
		}
		switch msg.Kind {
		case KindJWTJTI:
			c.target.InvalidateJWTJTILocal(msg.SubjectID)
		case KindJWTUser:
			c.target.InvalidateJWTUserLocal(msg.SubjectID)
		case KindRBACUser:
			c.target.InvalidateRBACUserLocal(msg.SubjectID)
		case KindRBACAll:
			c.target.InvalidateRBACAllLocal()
		}
	}
	c.setEpoch(domain, msg.Epoch)
	lag := time.Since(msg.Time).Seconds()
	if lag < 0 {
		lag = 0
	}
	lagGauge.WithLabelValues(domain).Set(lag)
	receiveTotal.WithLabelValues(string(msg.Kind), "ok").Inc()
}

func (c *Coordinator) invalidateJWTAll() {
	if c != nil && c.target != nil {
		c.target.InvalidateJWTAllLocal()
	}
}

func (c *Coordinator) invalidateRBACAll() {
	if c != nil && c.target != nil {
		c.target.InvalidateRBACAllLocal()
	}
}

func (c *Coordinator) markUnhealthy(message string, err error) {
	c.setHealthy(false)
	now := time.Now().UnixNano()
	last := c.lastLog.Load()
	if last == 0 || time.Duration(now-last) >= 30*time.Second {
		if c.lastLog.CompareAndSwap(last, now) {
			c.log.Warn("security cache coordinator unhealthy", "reason", message, "error", err)
		}
	}
}

func (c *Coordinator) setHealthy(v bool) {
	c.healthy.Store(v)
	if v {
		healthGauge.Set(1)
	} else {
		healthGauge.Set(0)
	}
}

func (c *Coordinator) epoch(domain string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epochs[domain]
}

func (c *Coordinator) setEpoch(domain string, epoch int64) {
	c.mu.Lock()
	if epoch > c.epochs[domain] {
		c.epochs[domain] = epoch
	}
	value := c.epochs[domain]
	c.mu.Unlock()
	epochGauge.WithLabelValues(domain).Set(float64(value))
}

func domainFor(kind Kind) (string, error) {
	switch kind {
	case KindJWTJTI, KindJWTUser:
		return "jwt", nil
	case KindRBACUser, KindRBACAll:
		return "rbac", nil
	}
	return "", fmt.Errorf("unsupported invalidation kind %q", kind)
}

func epochKey(domain string) string {
	if domain == "jwt" {
		return JWTEpochKey
	}
	return RBACEpochKey
}

func redisInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case string:
		var out int64
		_, _ = fmt.Sscan(n, &out)
		return out
	case []byte:
		var out int64
		_, _ = fmt.Sscan(string(n), &out)
		return out
	}
	return 0
}
