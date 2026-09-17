// Package controlplane owns control-plane health aggregation and policy
// evaluation independently of HTTP handlers and router composition.
package controlplane

import (
	"context"
	"sort"
)

// SummaryProvider is the narrow domain collaboration implemented by each
// controller. The service never stores or constructs concrete HTTP handlers.
type SummaryProvider interface {
	ControlPlaneSummary(context.Context) (map[string]any, error)
}

type Thresholds struct {
	QueueDepth    int32
	StaleRunning  int32
	RecentFailure int32
}

// Policy is the persistence-independent health policy consumed by Service.
type Policy struct {
	RecentFailureWindowMinutes int32
	Controllers                map[string]Thresholds
}

type Evaluation struct {
	Controller             string
	Summary                map[string]any
	Managed                bool
	QueueDepthExceeded     bool
	StaleRunningExceeded   bool
	RecentFailuresExceeded bool
}

type Snapshot struct {
	Controllers map[string]map[string]any
	Aggregate   map[string]any
	Evaluations []Evaluation
}

type Service struct {
	providers map[string]SummaryProvider
}

func NewService(providers map[string]SummaryProvider) *Service {
	owned := make(map[string]SummaryProvider, len(providers))
	for name, provider := range providers {
		if name != "" && provider != nil {
			owned[name] = provider
		}
	}
	return &Service{providers: owned}
}

// Snapshot collects controller state, applies policy, and produces the
// aggregate used by both the HTTP status endpoint and background evaluator.
func (s *Service) Snapshot(ctx context.Context, policy Policy) Snapshot {
	if s == nil {
		s = NewService(nil)
	}
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)

	result := Snapshot{Controllers: map[string]map[string]any{}}
	totalQueue, totalStale, failures, degraded := 0, 0, 0, 0
	for _, name := range names {
		summary, err := s.providers[name].ControlPlaneSummary(ctx)
		if err != nil {
			continue
		}
		summary = cloneSummary(summary)
		evaluation := applyPolicy(name, summary, policy)
		result.Controllers[name] = evaluation.Summary
		result.Evaluations = append(result.Evaluations, evaluation)
		totalQueue += queueDepth(evaluation.Summary)
		totalStale += staleRunning(evaluation.Summary)
		if hasLatestFailure(evaluation.Summary) {
			failures++
		}
		if controllerHealth(evaluation.Summary) != "healthy" {
			degraded++
		}
	}
	result.Aggregate = map[string]any{
		"controllers":             len(result.Controllers),
		"queueDepth":              totalQueue,
		"staleRunningCount":       totalStale,
		"controllersWithFailures": failures,
		"degradedControllers":     degraded,
		"health":                  health(degraded == 0),
	}
	return result
}

func applyPolicy(name string, summary map[string]any, policy Policy) Evaluation {
	thresholds, managed := policy.Controllers[name]
	evaluation := Evaluation{Controller: name, Summary: summary, Managed: managed}
	if !managed {
		if _, ok := summary["health"]; !ok {
			summary["health"] = "unknown"
		}
		if _, ok := summary["healthReasons"]; !ok {
			summary["healthReasons"] = []string{}
		}
		summary["policy"] = map[string]any{"managed": false}
		return evaluation
	}

	evaluation.QueueDepthExceeded = queueDepth(summary) >= int(thresholds.QueueDepth)
	evaluation.StaleRunningExceeded = staleRunning(summary) >= int(thresholds.StaleRunning)
	evaluation.RecentFailuresExceeded = recentFailures(summary) >= int(thresholds.RecentFailure)
	reasons := []string{}
	if evaluation.QueueDepthExceeded {
		reasons = append(reasons, "queue_depth")
	}
	if evaluation.StaleRunningExceeded {
		reasons = append(reasons, "stale_running")
	}
	if evaluation.RecentFailuresExceeded {
		reasons = append(reasons, "recent_failures")
	}
	summary["health"] = health(len(reasons) == 0)
	summary["healthReasons"] = reasons
	summary["policy"] = map[string]any{
		"managed":                    true,
		"queueDepthThreshold":        thresholds.QueueDepth,
		"staleRunningThreshold":      thresholds.StaleRunning,
		"recentFailureThreshold":     thresholds.RecentFailure,
		"recentFailureWindowMinutes": policy.RecentFailureWindowMinutes,
	}
	return evaluation
}

func cloneSummary(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source)+3)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func queueDepth(summary map[string]any) int {
	reconciler, ok := summary["reconciler"].(map[string]any)
	if !ok {
		return 0
	}
	return intValue(reconciler["queueDepth"])
}

func staleRunning(summary map[string]any) int {
	reconciler, ok := summary["reconciler"].(map[string]any)
	if !ok {
		return 0
	}
	return intValue(reconciler["staleRunningCount"])
}

func recentFailures(summary map[string]any) int { return intValue(summary["recentFailureCount"]) }

func controllerHealth(summary map[string]any) string {
	if value, ok := summary["health"].(string); ok {
		return value
	}
	return "unknown"
}

func hasLatestFailure(summary map[string]any) bool {
	value, ok := summary["latestFailure"]
	return ok && value != nil
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func health(healthy bool) string {
	if healthy {
		return "healthy"
	}
	return "degraded"
}
