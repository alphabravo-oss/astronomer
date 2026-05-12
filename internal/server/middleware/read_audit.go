// Migration 063 — read-side API audit middleware.
//
// ReadAudit emits a "read.*" audit row for every request matching at
// least one enabled read_audit_policy. HIPAA/PCI auditors require
// "who saw what credential and when"; mutation auditing alone doesn't
// answer that question.
//
// Wired AFTER auth (so we know the actor) but BEFORE the per-route
// handler. The middleware NEVER captures request or response body — we
// only persist the fact of the read. Bodies are a compliance landmine
// (secrets in them would end up double-logged).
//
// The emission is non-blocking: matched events are enqueued into a
// bounded buffered channel and drained by a single goroutine. When the
// channel is full, events are DROPPED and counted via
// astronomer_read_audit_emissions_total{outcome="dropped"} — the HTTP
// request must NEVER wait on the audit-log INSERT.

package middleware

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// hard-coded skip list — paths that should never participate in read
// audit even if a policy were to match. Healthz/readyz/metrics are
// already filtered by path_pattern (they don't match any default
// policy), but the explicit skip shaves cycles and protects against
// an operator mistakenly adding a wildcard policy.
var readAuditSkipPaths = map[string]bool{
	"/healthz":            true,
	"/readyz":             true,
	"/metrics":            true,
	"/api/v1/version/":    true,
}

// PolicyLister is the narrow DB surface the evaluator needs.
type PolicyLister interface {
	ListEnabledReadAuditPolicies(ctx context.Context) ([]sqlc.ReadAuditPolicy, error)
}

// PolicyEvaluator caches the enabled-policy list in-process and
// evaluates incoming requests against it. Cache TTL is 30s to match
// the maintenance-window evaluator. Admin writes invalidate the cache.
type PolicyEvaluator struct {
	store     PolicyLister
	ttl       time.Duration
	clock     func() time.Time
	rng       *rand.Rand
	rngMu     sync.Mutex
	mu        sync.RWMutex
	policies  []sqlc.ReadAuditPolicy
	fetchedAt time.Time
}

// NewPolicyEvaluator wires an evaluator with a 30s TTL.
func NewPolicyEvaluator(store PolicyLister) *PolicyEvaluator {
	return &PolicyEvaluator{
		store: store,
		ttl:   30 * time.Second,
		clock: time.Now,
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// ListPolicies returns the cached enabled policies; refreshes when the
// TTL has elapsed. Safe for concurrent use.
func (e *PolicyEvaluator) ListPolicies(ctx context.Context) []sqlc.ReadAuditPolicy {
	e.mu.RLock()
	if !e.fetchedAt.IsZero() && e.clock().Sub(e.fetchedAt) < e.ttl {
		out := e.policies
		e.mu.RUnlock()
		return out
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// Double-check under the write lock — another goroutine may have
	// refreshed while we were waiting.
	if !e.fetchedAt.IsZero() && e.clock().Sub(e.fetchedAt) < e.ttl {
		return e.policies
	}
	if e.store == nil {
		e.policies = nil
		e.fetchedAt = e.clock()
		return nil
	}
	rows, err := e.store.ListEnabledReadAuditPolicies(ctx)
	if err != nil {
		// Soft-fail: keep the previous cache. The next request retries.
		// We don't want a DB blip to disable audit silently — but we
		// also don't want to block on retries.
		slog.Default().Warn("read-audit policy refresh failed", "error", err)
		return e.policies
	}
	e.policies = rows
	e.fetchedAt = e.clock()
	return rows
}

// Invalidate forces the next ListPolicies call to refetch.
func (e *PolicyEvaluator) Invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fetchedAt = time.Time{}
}

// Match returns the first enabled policy whose path_pattern + verbs
// matches the given route + method, or nil if no policy applies. The
// pattern compare is "starts with" against the chi route pattern (not
// the raw URL) so delete-after-pattern doesn't bypass audit by varying
// the UUID.
func (e *PolicyEvaluator) Match(ctx context.Context, routePattern, method string) *sqlc.ReadAuditPolicy {
	policies := e.ListPolicies(ctx)
	if len(policies) == 0 {
		return nil
	}
	method = strings.ToUpper(method)
	for i := range policies {
		p := &policies[i]
		if !verbMatches(p.Verbs, method) {
			continue
		}
		if !pathMatches(p.PathPattern, routePattern) {
			continue
		}
		return p
	}
	return nil
}

// Sample returns true when the policy's sample_rate fires for the
// current draw. SampleRate of 1.0 always fires; 0.0 never does;
// anything in between fires roughly that fraction over a long window.
func (e *PolicyEvaluator) Sample(p *sqlc.ReadAuditPolicy) bool {
	if p == nil {
		return false
	}
	if p.SampleRate >= 1.0 {
		return true
	}
	if p.SampleRate <= 0.0 {
		return false
	}
	e.rngMu.Lock()
	defer e.rngMu.Unlock()
	return e.rng.Float64() <= p.SampleRate
}

// verbMatches checks whether method is in the verbs list. "*" matches
// any. Verbs is a comma-separated list (typical: "GET" or "GET,HEAD").
func verbMatches(verbs, method string) bool {
	verbs = strings.TrimSpace(verbs)
	if verbs == "" || verbs == "*" {
		return true
	}
	for _, v := range strings.Split(verbs, ",") {
		if strings.EqualFold(strings.TrimSpace(v), method) {
			return true
		}
	}
	return false
}

// pathMatches checks whether routePattern starts with the policy
// pattern. The wildcard "*" matches any path. Patterns containing
// internal "*" segments (e.g. "/projects/*/cloud-credentials") are
// treated as "prefix-then-suffix" — the "*" segment matches any single
// URL path segment.
func pathMatches(pattern, route string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	// Normalize away any "/api/v1" prefix on the route so policy patterns
	// like "/admin/sso" match the chi-mounted route /api/v1/admin/sso/.
	r := strings.TrimPrefix(route, "/api/v1")
	r = strings.TrimSuffix(r, "/")
	p := strings.TrimSuffix(pattern, "/")

	if !strings.Contains(p, "*") {
		return strings.HasPrefix(r, p)
	}

	// Split on "*" and walk each fragment in order through the route
	// string. Each "*" stands for one path segment (no slash).
	parts := strings.Split(p, "*")
	pos := 0
	for i, frag := range parts {
		if frag == "" {
			// Adjacent "*", or leading/trailing "*". Skip — the next
			// non-empty frag handles the actual match.
			if i == 0 {
				continue
			}
			// Skip a single segment.
			next := strings.IndexByte(r[pos:], '/')
			if next == -1 {
				return false
			}
			pos += next + 1
			continue
		}
		idx := strings.Index(r[pos:], frag)
		if idx == -1 {
			return false
		}
		pos += idx + len(frag)
	}
	return true
}

// AuditEnqueuer is the narrow surface the middleware uses to hand off
// rows. *sqlc.Queries satisfies it directly.
type AuditEnqueuer interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// readAuditQueue is the bounded buffered channel + worker goroutine
// behind ReadAudit. One per process; the worker drains rows to the
// audit_log table without blocking the request hot path.
type readAuditQueue struct {
	enq    AuditEnqueuer
	in     chan sqlc.CreateAuditLogV1Params
	drops  atomic.Uint64
	wg     sync.WaitGroup
	stopCh chan struct{}
	depth  atomic.Int64
}

func newReadAuditQueue(enq AuditEnqueuer, buffer int) *readAuditQueue {
	if buffer <= 0 {
		buffer = 1024
	}
	q := &readAuditQueue{
		enq:    enq,
		in:     make(chan sqlc.CreateAuditLogV1Params, buffer),
		stopCh: make(chan struct{}),
	}
	q.wg.Add(1)
	go q.drain()
	return q
}

func (q *readAuditQueue) drain() {
	defer q.wg.Done()
	for {
		select {
		case row, ok := <-q.in:
			if !ok {
				return
			}
			q.depth.Add(-1)
			updateReadAuditQueueDepth(q.depth.Load())
			if q.enq != nil {
				// Use a short bounded context so a stuck DB doesn't
				// pin the worker indefinitely.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := q.enq.CreateAuditLogV1(ctx, row); err != nil {
					slog.Default().Warn("read-audit insert failed",
						"action", row.Action,
						"path", row.Path,
						"error", err,
					)
					recordReadAuditEmission(row.Action, "error")
				} else {
					recordReadAuditEmission(row.Action, "recorded")
				}
				cancel()
			}
		case <-q.stopCh:
			return
		}
	}
}

// enqueue hands a row to the worker, dropping when the channel is
// full. Returns true when enqueued, false when dropped.
func (q *readAuditQueue) enqueue(row sqlc.CreateAuditLogV1Params) bool {
	select {
	case q.in <- row:
		q.depth.Add(1)
		updateReadAuditQueueDepth(q.depth.Load())
		return true
	default:
		n := q.drops.Add(1)
		if n == 1 || n%1000 == 0 {
			slog.Default().Warn("read-audit queue full — dropping event",
				"action", row.Action,
				"path", row.Path,
				"total_drops", n,
			)
		}
		recordReadAuditEmission(row.Action, "dropped")
		return false
	}
}

// Shutdown signals the worker to exit and waits for the final drain.
func (q *readAuditQueue) Shutdown() {
	close(q.stopCh)
	q.wg.Wait()
}

// ReadAudit returns a chi middleware that records an audit row for
// every request matching at least one enabled read_audit_policy.
//
// Wire AFTER auth (so we have an actor) and BEFORE the per-route
// handler. Pass enq=nil to disable persistence (the middleware still
// matches and logs).
func ReadAudit(eval *PolicyEvaluator, enq AuditEnqueuer) func(http.Handler) http.Handler {
	queue := newReadAuditQueue(enq, 1024)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if readAuditSkipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)

			// Resolve route pattern AFTER the handler has run — chi
			// fills RouteContext.RoutePattern() as part of routing,
			// not before.
			routePattern := chiRoutePattern(r)
			if routePattern == "" {
				routePattern = r.URL.Path
			}
			pol := eval.Match(r.Context(), routePattern, r.Method)
			if pol == nil {
				return
			}
			if !eval.Sample(pol) {
				recordReadAuditEmission(buildReadAuditAction(routePattern), "sampled_out")
				return
			}

			action := buildReadAuditAction(routePattern)
			row := buildReadAuditRow(r, sw.status, routePattern, pol, action)
			queue.enqueue(row)
		})
	}
}

func chiRoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return ""
	}
	if p := rctx.RoutePattern(); p != "" {
		return p
	}
	return ""
}

// buildReadAuditAction renders the action string for a matched read.
// We slug the route pattern so a chi route like /projects/{id}/cloud-
// credentials becomes "read.projects.cloud_credentials". Compliance
// reports group by action prefix, so the deterministic shape matters.
func buildReadAuditAction(routePattern string) string {
	p := strings.TrimPrefix(routePattern, "/api/v1/")
	p = strings.Trim(p, "/")
	if p == "" {
		return "read.root"
	}
	// Drop chi pattern variables (anything inside {}).
	segs := strings.Split(p, "/")
	keep := make([]string, 0, len(segs))
	for _, s := range segs {
		if strings.HasPrefix(s, "{") {
			continue
		}
		s = strings.ReplaceAll(s, "-", "_")
		keep = append(keep, s)
	}
	return "read." + strings.Join(keep, ".")
}

func buildReadAuditRow(r *http.Request, status int, routePattern string, pol *sqlc.ReadAuditPolicy, action string) sqlc.CreateAuditLogV1Params {
	detail := map[string]any{
		"method":          r.Method,
		"path_pattern":    routePattern,
		"raw_path":        r.URL.Path,
		"policy_id":       pol.ID.String(),
		"policy_name":     pol.Name,
		"response_status": status,
	}
	rawDetail := audit.SanitizeDetail(detail)
	// Re-marshal so the JSON column gets a stable JSON.RawMessage.
	js := mustMarshalDetail(rawDetail)
	return sqlc.CreateAuditLogV1Params{
		Source:          "http",
		CorrelationID:   GetCorrelationID(r.Context()),
		UserID:          currentUserUUID(r.Context()),
		ActorAuthMethod: authMethod(r.Context()),
		Action:          action,
		ResourceType:    "",
		ResourceID:      "",
		ResourceName:    "",
		HTTPMethod:      r.Method,
		Path:            r.URL.Path,
		StatusCode:      int32(status),
		DurationMs:      0,
		RequestID:       GetRequestID(r.Context()),
		IpAddress:       remoteIPAddr(r),
		UserAgent:       r.UserAgent(),
		Detail:          js,
		ActionClass:     "read",
	}
}

