// Package maintenance implements operator-defined maintenance windows
// that gate destructive operations across the management plane.
//
// The evaluator's job is to answer "for this (operation_type, cluster)
// combo, is there an active maintenance window blocking it?" The
// answer drives the mutation handler's response: refuse (409) or defer
// (202 + queued execution) per the matched window's on_block field.
//
// The Window evaluator reads ListEnabledMaintenanceWindows() into an
// in-memory cache (30s TTL); the cache is invalidated on PUT/DELETE in
// the admin handler so operator changes take effect immediately. The
// per-mutation check is one map lookup + cron evaluation, so the
// overhead added to every gated mutation is negligible.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Mode constants. Mirror the CHECK constraint on maintenance_windows.mode.
const (
	ModeBlackout  = "blackout"
	ModePermitted = "permitted"
)

// OnBlock constants. Mirror the CHECK constraint on maintenance_windows.on_block.
const (
	OnBlockRefuse = "refuse"
	OnBlockDefer  = "defer"
)

// Operation type constants. These string keys are what handlers pass
// in to IsBlocked and what operators put in operation_types JSONB.
// Kept here so both sides agree on spelling.
const (
	OpClusterDelete         = "cluster.delete"
	OpProjectDelete         = "project.delete"
	OpToolInstall           = "tool.install"
	OpToolUpgrade           = "tool.upgrade"
	OpToolUninstall         = "tool.uninstall"
	OpHelmInstall           = "helm.install"
	OpHelmUninstall         = "helm.uninstall"
	OpClusterTemplateApply  = "cluster_template.apply"
)

// KnownOperationTypes is the set of operation types the platform currently
// recognises as "destructive". Helpers in the admin handler use it to
// validate operator-supplied operation_types values up front (rather than
// silently letting a typo slip past until a mutation flies under the
// radar).
var KnownOperationTypes = []string{
	OpClusterDelete,
	OpProjectDelete,
	OpToolInstall,
	OpToolUpgrade,
	OpToolUninstall,
	OpHelmInstall,
	OpHelmUninstall,
	OpClusterTemplateApply,
}

// Window is the package-local mirror of sqlc.MaintenanceWindow with the
// JSONB fields parsed once. The evaluator works on these so each
// IsBlocked call avoids re-unmarshaling on the hot path.
type Window struct {
	ID              uuid.UUID
	Name            string
	Mode            string
	CronOpen        string
	DurationMinutes int
	Timezone        string
	ClusterSelector map[string]string
	OperationTypes  []string
	OnBlock         string
	Enabled         bool
}

// FromSQLC decodes a sqlc.MaintenanceWindow into the package-local Window
// shape. Errors propagate so the evaluator can degrade gracefully on a
// malformed row (typically: log + skip rather than fail-closed).
func FromSQLC(row sqlc.MaintenanceWindow) (Window, error) {
	sel := map[string]string{}
	if len(row.ClusterSelector) > 0 && string(row.ClusterSelector) != "{}" {
		if err := json.Unmarshal(row.ClusterSelector, &sel); err != nil {
			return Window{}, fmt.Errorf("decode cluster_selector for window %s: %w", row.Name, err)
		}
	}
	ops := []string{}
	if len(row.OperationTypes) > 0 && string(row.OperationTypes) != "[]" {
		if err := json.Unmarshal(row.OperationTypes, &ops); err != nil {
			return Window{}, fmt.Errorf("decode operation_types for window %s: %w", row.Name, err)
		}
	}
	return Window{
		ID:              row.ID,
		Name:            row.Name,
		Mode:            row.Mode,
		CronOpen:        row.CronOpen,
		DurationMinutes: int(row.DurationMinutes),
		Timezone:        row.Timezone,
		ClusterSelector: sel,
		OperationTypes:  ops,
		OnBlock:         row.OnBlock,
		Enabled:         row.Enabled,
	}, nil
}

// WindowEvaluator wraps the DB read + in-memory cache for the list of
// enabled windows. Cached for 30s; PUT/DELETE on a window invalidates
// the cache. *Evaluator below satisfies this for production; tests use
// a fixedEvaluator from the test package.
type WindowEvaluator interface {
	Windows(ctx context.Context) ([]Window, error)
}

// WindowQuerier is the sqlc surface the evaluator's cache needs.
// *sqlc.Queries satisfies this directly.
type WindowQuerier interface {
	ListEnabledMaintenanceWindows(ctx context.Context) ([]sqlc.MaintenanceWindow, error)
}

// Evaluator is the production WindowEvaluator. Holds a 30s TTL cache of
// the enabled-windows slice. Safe for concurrent use.
type Evaluator struct {
	queries WindowQuerier
	ttl     time.Duration

	mu        sync.RWMutex
	cached    []Window
	cachedAt  time.Time
	cacheHits int
}

// NewEvaluator builds an Evaluator with the default 30s TTL.
func NewEvaluator(queries WindowQuerier) *Evaluator {
	return &Evaluator{queries: queries, ttl: 30 * time.Second}
}

// NewEvaluatorWithTTL allows tests to use a shorter cache window.
func NewEvaluatorWithTTL(queries WindowQuerier, ttl time.Duration) *Evaluator {
	return &Evaluator{queries: queries, ttl: ttl}
}

// Windows returns the current set of enabled maintenance windows. The
// underlying DB read happens once per ttl; concurrent callers within
// the TTL share the cached slice (read-only).
func (e *Evaluator) Windows(ctx context.Context) ([]Window, error) {
	if e == nil || e.queries == nil {
		return nil, nil
	}
	e.mu.RLock()
	if !e.cachedAt.IsZero() && time.Since(e.cachedAt) < e.ttl {
		out := e.cached
		e.mu.RUnlock()
		return out, nil
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// Double-check: a concurrent caller may have refreshed while we
	// were waiting on the write lock.
	if !e.cachedAt.IsZero() && time.Since(e.cachedAt) < e.ttl {
		return e.cached, nil
	}
	rows, err := e.queries.ListEnabledMaintenanceWindows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Window, 0, len(rows))
	for _, row := range rows {
		w, err := FromSQLC(row)
		if err != nil {
			// Skip malformed rows rather than fail-closed. The metric
			// + log path is the operator's signal; gating every
			// mutation on a corrupt JSONB blob would be worse than
			// letting that one window be ignored.
			continue
		}
		out = append(out, w)
	}
	e.cached = out
	e.cachedAt = time.Now()
	return out, nil
}

// Invalidate drops the cache so the next Windows() call hits the DB.
// Called by the admin handler on POST/PUT/DELETE.
func (e *Evaluator) Invalidate() {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cached = nil
	e.cachedAt = time.Time{}
}

// IsBlocked returns true when a gated operation should be blocked by an
// active window. opType is one of the Op* constants; clusterLabels are
// the parsed labels of the target cluster (empty for non-cluster-scoped
// ops). The matching Window is returned so the handler can choose
// refuse vs defer + compute the deferred_until / next_open time.
//
// Multiple matching windows: the first one that says "blocked" wins.
// We prefer blackout matches over permitted matches because the
// blackout semantics are simpler to reason about for the operator —
// "do nothing during this window" beats "only do this during this
// window" when both happen to fire.
func IsBlocked(ctx context.Context, ev WindowEvaluator, opType string, clusterLabels map[string]string, now time.Time) (bool, *Window, error) {
	if ev == nil {
		return false, nil, nil
	}
	windows, err := ev.Windows(ctx)
	if err != nil {
		return false, nil, err
	}
	// First pass: blackout windows that are currently active.
	for i := range windows {
		w := windows[i]
		if !w.Enabled {
			continue
		}
		if w.Mode != ModeBlackout {
			continue
		}
		if !appliesToOp(w, opType) {
			continue
		}
		if !appliesToCluster(w, clusterLabels) {
			continue
		}
		active, err := IsActive(w, now)
		if err != nil {
			continue
		}
		if active {
			return true, &w, nil
		}
	}
	// Second pass: permitted windows that are currently inactive.
	for i := range windows {
		w := windows[i]
		if !w.Enabled {
			continue
		}
		if w.Mode != ModePermitted {
			continue
		}
		if !appliesToOp(w, opType) {
			continue
		}
		if !appliesToCluster(w, clusterLabels) {
			continue
		}
		active, err := IsActive(w, now)
		if err != nil {
			continue
		}
		if !active {
			return true, &w, nil
		}
	}
	return false, nil, nil
}

// appliesToOp returns true when the window's operation_types matches
// the candidate op. Empty list = match everything destructive.
func appliesToOp(w Window, opType string) bool {
	if len(w.OperationTypes) == 0 {
		return true
	}
	for _, t := range w.OperationTypes {
		if t == opType {
			return true
		}
	}
	return false
}

// appliesToCluster returns true when the window's cluster_selector
// matches the candidate cluster's labels. Empty selector = all
// clusters. Non-empty selector: every (key, value) in the selector
// must equal the cluster label.
func appliesToCluster(w Window, clusterLabels map[string]string) bool {
	if len(w.ClusterSelector) == 0 {
		return true
	}
	if len(clusterLabels) == 0 {
		// Non-empty selector + no labels = no match. The window scopes
		// to clusters carrying the required labels; an unlabeled
		// target shouldn't trip a labeled scope.
		return false
	}
	for k, v := range w.ClusterSelector {
		if clusterLabels[k] != v {
			return false
		}
	}
	return true
}

// IsActive returns true when now falls inside the most recent open of
// w's cron expression + duration_minutes minutes. Timezone-aware: the
// cron is interpreted in w.Timezone.
//
// Implementation: walk the previous fire of the cron (we don't have a
// direct "Prev" method, so we Next() from a point earlier than now
// minus duration; the latest fire before now is the window's open
// time). If now - open < duration, the window is active.
func IsActive(w Window, now time.Time) (bool, error) {
	loc, err := loadLocation(w.Timezone)
	if err != nil {
		return false, fmt.Errorf("load timezone %q: %w", w.Timezone, err)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	expr, err := parser.Parse(strings.TrimSpace(w.CronOpen))
	if err != nil {
		return false, fmt.Errorf("parse cron %q: %w", w.CronOpen, err)
	}
	dur := time.Duration(w.DurationMinutes) * time.Minute
	if dur <= 0 {
		return false, nil
	}
	// To find the most recent open before now, step Next() forward
	// from a time that's guaranteed to be earlier than the most recent
	// fire (now - 366d covers any sane cron). The last Next() result
	// before now is the most recent open.
	nowLoc := now.In(loc)
	cursor := nowLoc.Add(-366 * 24 * time.Hour)
	var lastOpen time.Time
	for {
		next := expr.Next(cursor)
		if next.IsZero() || next.After(nowLoc) {
			break
		}
		lastOpen = next
		cursor = next
	}
	if lastOpen.IsZero() {
		return false, nil
	}
	return nowLoc.Sub(lastOpen) < dur, nil
}

// NextOpen returns the next time the window will open (i.e. the next
// time its cron expression fires, regardless of whether the window is
// currently active). Used by the defer path to set deferred_until.
func NextOpen(w Window, now time.Time) time.Time {
	loc, err := loadLocation(w.Timezone)
	if err != nil {
		return time.Time{}
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	expr, err := parser.Parse(strings.TrimSpace(w.CronOpen))
	if err != nil {
		return time.Time{}
	}
	return expr.Next(now.In(loc))
}

// NextClose returns the time the currently-active window will close.
// Caller's responsibility to only invoke when w is active; otherwise
// the returned value isn't meaningful.
func NextClose(w Window, now time.Time) time.Time {
	loc, err := loadLocation(w.Timezone)
	if err != nil {
		return time.Time{}
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	expr, err := parser.Parse(strings.TrimSpace(w.CronOpen))
	if err != nil {
		return time.Time{}
	}
	dur := time.Duration(w.DurationMinutes) * time.Minute
	nowLoc := now.In(loc)
	cursor := nowLoc.Add(-366 * 24 * time.Hour)
	var lastOpen time.Time
	for {
		next := expr.Next(cursor)
		if next.IsZero() || next.After(nowLoc) {
			break
		}
		lastOpen = next
		cursor = next
	}
	if lastOpen.IsZero() {
		return time.Time{}
	}
	return lastOpen.Add(dur)
}

// loadLocation resolves an IANA timezone name to a *time.Location.
// Empty string is treated as UTC.
func loadLocation(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}
