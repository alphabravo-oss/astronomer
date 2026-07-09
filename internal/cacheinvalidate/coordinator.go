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
	"strconv"
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
	writeTimeout  = 2 * time.Second
)

// Kind identifies the narrow local invalidation to apply.
type Kind string

const (
	KindJWTJTI   Kind = "jwt_jti"
	KindJWTUser  Kind = "jwt_user"
	KindJWTAll   Kind = "jwt_all"
	KindRBACUser Kind = "rbac_user"
	KindRBACAll  Kind = "rbac_all"
)

type pendingInvalidation struct {
	kind      Kind
	subjectID string
	inFlight  bool
}

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
	flushMu sync.Mutex
	mu      sync.Mutex
	epochs  map[string]int64
	pending map[string][]pendingInvalidation
	recover map[string]int64
	// conservativeGeneration advances whenever health is lost. Reconciliation
	// must clear both local cache domains and record that generation before
	// positive cache hits may resume.
	conservativeGeneration uint64
	reconciledGeneration   uint64
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
	return &Coordinator{
		rdb: rdb, target: target, origin: origin, period: period, log: log,
		epochs:  map[string]int64{"jwt": 0, "rbac": 0},
		pending: map[string][]pendingInvalidation{"jwt": nil, "rbac": nil},
		recover: map[string]int64{"jwt": 0, "rbac": 0},
	}
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

// Broadcast runs after the caller has invalidated its local cache. It records
// the mutation in a bounded in-memory pending queue before attempting Redis
// with a post-commit context independent of request cancellation. The pending
// entry is retained until INCR durably advances the domain epoch; publication
// is then best-effort because polling that epoch provides the recovery path.
func (c *Coordinator) Broadcast(_ context.Context, kind Kind, subjectID string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	domain, err := domainFor(kind)
	if err != nil {
		return err
	}
	if requiresSubject(kind) && subjectID == "" {
		return fmt.Errorf("subject_id is required for invalidation kind %q", kind)
	}
	c.enqueue(domain, kind, subjectID)
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.flushPending(ctx)
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
		if err := c.maintain(ctx, true); err != nil {
			c.markUnhealthy("initial epoch reconciliation failed", err)
			_ = pubsub.Close()
			c.wait(ctx)
			continue
		}
		c.setHealthyIfQuiescent()
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
				if err := c.maintain(ctx, false); err != nil {
					c.markUnhealthy("security cache maintenance failed", err)
					continue
				}
				c.setHealthyIfQuiescent()
			}
		}
		ticker.Stop()
		_ = pubsub.Close()
		c.wait(ctx)
	}
}

func (c *Coordinator) maintain(ctx context.Context, reconnect bool) error {
	flushErr := c.flushPending(ctx)
	reconcileErr := c.reconcile(ctx, reconnect)
	return errors.Join(flushErr, reconcileErr)
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
	if len(values) != 2 {
		return fmt.Errorf("epoch reconciliation returned %d values, want 2", len(values))
	}
	jwtEpoch, err := strictRedisEpoch(values[0])
	if err != nil {
		return fmt.Errorf("invalid JWT epoch: %w", err)
	}
	rbacEpoch, err := strictRedisEpoch(values[1])
	if err != nil {
		return fmt.Errorf("invalid RBAC epoch: %w", err)
	}
	remote := map[string]int64{"jwt": jwtEpoch, "rbac": rbacEpoch}
	c.mu.Lock()
	conservativeGeneration := c.conservativeGeneration
	needsConservative := conservativeGeneration > c.reconciledGeneration
	c.mu.Unlock()
	if reconnect || needsConservative {
		// Invalidate before re-enabling cache hits even when epochs are equal: a
		// disconnect may have overlapped an unobservable mutation.
		c.invalidateJWTAll()
		c.invalidateRBACAll()
	}
	for _, domain := range []string{"jwt", "rbac"} {
		local := c.epoch(domain)
		if remote[domain] < local {
			return fmt.Errorf("%s epoch regressed from %d to %d", domain, local, remote[domain])
		}
		required := c.recoveryEpoch(domain)
		if remote[domain] < required {
			return fmt.Errorf("%s epoch %d has not reached durable pending epoch %d", domain, remote[domain], required)
		}
		if remote[domain] > local || required > 0 {
			if domain == "jwt" {
				c.invalidateJWTAll()
			} else {
				c.invalidateRBACAll()
			}
		}
		c.setEpoch(domain, remote[domain])
		c.clearRecovery(domain, remote[domain])
	}
	c.mu.Lock()
	if conservativeGeneration > c.reconciledGeneration {
		c.reconciledGeneration = conservativeGeneration
	}
	c.mu.Unlock()
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
	if requiresSubject(msg.Kind) && msg.SubjectID == "" {
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
		case KindJWTAll:
			c.target.InvalidateJWTAllLocal()
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
	c.mu.Lock()
	c.conservativeGeneration++
	c.healthy.Store(false)
	c.mu.Unlock()
	healthGauge.Set(0)
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

func (c *Coordinator) setHealthyIfQuiescent() {
	c.mu.Lock()
	quiescent := len(c.pending["jwt"]) == 0 && len(c.pending["rbac"]) == 0 && c.recover["jwt"] == 0 && c.recover["rbac"] == 0 && c.conservativeGeneration == c.reconciledGeneration
	if quiescent {
		c.healthy.Store(true)
	}
	c.mu.Unlock()
	if quiescent {
		healthGauge.Set(1)
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

func (c *Coordinator) enqueue(domain string, kind Kind, subjectID string) {
	c.mu.Lock()
	queue := c.pending[domain]
	if len(queue) > 0 && !queue[len(queue)-1].inFlight {
		queue[len(queue)-1] = mergePending(domain, queue[len(queue)-1], pendingInvalidation{kind: kind, subjectID: subjectID})
	} else {
		queue = append(queue, pendingInvalidation{kind: kind, subjectID: subjectID})
	}
	c.pending[domain] = queue
	c.healthy.Store(false)
	c.mu.Unlock()
	healthGauge.Set(0)
}

func mergePending(domain string, existing, next pendingInvalidation) pendingInvalidation {
	if existing.kind == next.kind && existing.subjectID == next.subjectID {
		return existing
	}
	if domain == "jwt" {
		return pendingInvalidation{kind: KindJWTAll}
	}
	return pendingInvalidation{kind: KindRBACAll}
}

func (c *Coordinator) flushPending(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	c.flushMu.Lock()
	defer c.flushMu.Unlock()
	for {
		domain, item, ok := c.claimPending()
		if !ok {
			return nil
		}
		epoch, err := c.rdb.Incr(ctx, epochKey(domain)).Result()
		if err != nil {
			c.releasePending(domain)
			c.markUnhealthy("epoch increment failed", err)
			publishTotal.WithLabelValues(string(item.kind), "error").Inc()
			return fmt.Errorf("increment %s invalidation epoch: %w", domain, err)
		}
		c.recordDurable(domain, epoch)
		c.invalidateDomain(domain)

		msg := Message{Version: WireVersion, Kind: item.kind, SubjectID: item.subjectID, Epoch: epoch, Origin: c.origin, Time: time.Now().UTC()}
		payload, err := json.Marshal(msg)
		if err != nil {
			c.markUnhealthy("encode invalidation failed", err)
			publishTotal.WithLabelValues(string(item.kind), "error").Inc()
			return fmt.Errorf("encode invalidation: %w", err)
		}
		if err := c.rdb.Publish(ctx, Channel, payload).Err(); err != nil {
			c.markUnhealthy("publish failed", err)
			publishTotal.WithLabelValues(string(item.kind), "error").Inc()
			return fmt.Errorf("publish invalidation: %w", err)
		}
		publishTotal.WithLabelValues(string(item.kind), "ok").Inc()
	}
}

func (c *Coordinator) claimPending() (string, pendingInvalidation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, domain := range []string{"jwt", "rbac"} {
		if len(c.pending[domain]) == 0 {
			continue
		}
		c.pending[domain][0].inFlight = true
		return domain, c.pending[domain][0], true
	}
	return "", pendingInvalidation{}, false
}

func (c *Coordinator) releasePending(domain string) {
	c.mu.Lock()
	if len(c.pending[domain]) > 0 {
		c.pending[domain][0].inFlight = false
	}
	c.mu.Unlock()
}

func (c *Coordinator) recordDurable(domain string, epoch int64) {
	c.mu.Lock()
	if len(c.pending[domain]) > 0 && c.pending[domain][0].inFlight {
		c.pending[domain] = c.pending[domain][1:]
	}
	if epoch > c.recover[domain] {
		c.recover[domain] = epoch
	}
	c.mu.Unlock()
}

func (c *Coordinator) recoveryEpoch(domain string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.recover[domain]
}

func (c *Coordinator) clearRecovery(domain string, observed int64) {
	c.mu.Lock()
	if c.recover[domain] > 0 && observed >= c.recover[domain] {
		c.recover[domain] = 0
	}
	c.mu.Unlock()
}

func (c *Coordinator) invalidateDomain(domain string) {
	if domain == "jwt" {
		c.invalidateJWTAll()
	} else {
		c.invalidateRBACAll()
	}
}

func domainFor(kind Kind) (string, error) {
	switch kind {
	case KindJWTJTI, KindJWTUser, KindJWTAll:
		return "jwt", nil
	case KindRBACUser, KindRBACAll:
		return "rbac", nil
	}
	return "", fmt.Errorf("unsupported invalidation kind %q", kind)
}

func requiresSubject(kind Kind) bool {
	return kind != KindJWTAll && kind != KindRBACAll
}

func epochKey(domain string) string {
	if domain == "jwt" {
		return JWTEpochKey
	}
	return RBACEpochKey
}

func strictRedisEpoch(v any) (int64, error) {
	if v == nil {
		return 0, nil
	}
	var raw string
	switch value := v.(type) {
	case string:
		raw = value
	case []byte:
		raw = string(value)
	case int64:
		if value < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return value, nil
	default:
		return 0, fmt.Errorf("unsupported value type %T", v)
	}
	if raw == "" {
		return 0, fmt.Errorf("empty value")
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("not an integer")
	}
	if epoch < 0 {
		return 0, fmt.Errorf("negative value")
	}
	return epoch, nil
}
