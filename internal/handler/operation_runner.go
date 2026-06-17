package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/operationstate"
)

const operationSupersededMessage = "superseded by newer operation for target"
const operationRetryInvalidStateMessage = "Only failed or superseded operations can be retried"

type operationRunnerConfig[T any] struct {
	ID              func(T) uuid.UUID
	TargetKey       func(T) string
	Status          func(T) string
	ShouldSupersede func(T, time.Time) bool
	IsFreshRunning  func(T, time.Time) bool
	Supersede       func(context.Context, T)
	MarkRunning     func(context.Context, T) (T, error)
	Claimed         func(T) claimedOp
	Now             func() time.Time
}

type operationStatusSummaryConfig[T any] struct {
	Status                func(T) string
	CreatedAt             func(T) time.Time
	IsStaleRunning        func(T, time.Time) bool
	Include               func(context.Context, T) bool
	Preview               func(context.Context, T) map[string]any
	IsFailure             func(T) bool
	Now                   func() time.Time
	RecentLimit           int
	RecentFailureWindow   time.Duration
	StaleThresholdSeconds int
}

type operationStatusSummary struct {
	Counts                map[string]int
	QueueDepth            int
	StaleRunning          int
	StaleThresholdSeconds int
	RecentFailures        int
	Recent                []map[string]any
	LatestFailure         map[string]any
}

func claimLatestOperations[T any](ctx context.Context, ops []T, cfg operationRunnerConfig[T]) []claimedOp {
	if len(ops) == 0 {
		return nil
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	latestByTarget := map[string]uuid.UUID{}
	for i := len(ops) - 1; i >= 0; i-- {
		key := cfg.TargetKey(ops[i])
		if _, ok := latestByTarget[key]; !ok {
			latestByTarget[key] = cfg.ID(ops[i])
		}
	}
	claimed := make([]claimedOp, 0, len(ops))
	for _, op := range ops {
		key := cfg.TargetKey(op)
		if latestID, ok := latestByTarget[key]; ok && latestID != cfg.ID(op) {
			shouldSupersede := true
			if cfg.ShouldSupersede != nil {
				shouldSupersede = cfg.ShouldSupersede(op, now())
			}
			if shouldSupersede {
				cfg.Supersede(ctx, op)
			}
			continue
		}
		if cfg.Status(op) == OpStatusRunning && cfg.IsFreshRunning != nil && cfg.IsFreshRunning(op, now()) {
			continue
		}
		running, err := cfg.MarkRunning(ctx, op)
		if err != nil {
			continue
		}
		claimed = append(claimed, cfg.Claimed(running))
	}
	return claimed
}

func isRetryableOperationStatus(status string) bool {
	return operationstate.IsRetryable(status)
}

func requireRetryableOperation(w http.ResponseWriter, r *http.Request, status string) bool {
	if isRetryableOperationStatus(status) {
		return true
	}
	RespondRequestError(w, r, http.StatusConflict, apierror.InvalidState, operationRetryInvalidStateMessage)
	return false
}

func summarizeOperations[T any](ctx context.Context, ops []T, cfg operationStatusSummaryConfig[T]) operationStatusSummary {
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	recentLimit := cfg.RecentLimit
	if recentLimit <= 0 {
		recentLimit = 5
	}
	failureWindow := cfg.RecentFailureWindow
	if failureWindow <= 0 {
		failureWindow = 30 * time.Minute
	}
	isFailure := cfg.IsFailure
	if isFailure == nil {
		isFailure = func(op T) bool {
			return operationstate.IsFailure(cfg.Status(op))
		}
	}
	summary := operationStatusSummary{
		Counts:                map[string]int{},
		Recent:                make([]map[string]any, 0, min(len(ops), recentLimit)),
		StaleThresholdSeconds: cfg.StaleThresholdSeconds,
	}
	for _, op := range ops {
		if cfg.Include != nil && !cfg.Include(ctx, op) {
			continue
		}
		status := cfg.Status(op)
		summary.Counts[status]++
		if status == OpStatusRunning && cfg.IsStaleRunning != nil && cfg.IsStaleRunning(op, now()) {
			summary.StaleRunning++
		}
		if len(summary.Recent) < recentLimit && cfg.Preview != nil {
			summary.Recent = append(summary.Recent, cfg.Preview(ctx, op))
		}
		if isFailure(op) {
			if now().Sub(cfg.CreatedAt(op)) <= failureWindow {
				summary.RecentFailures++
			}
			if summary.LatestFailure == nil && cfg.Preview != nil {
				summary.LatestFailure = cfg.Preview(ctx, op)
			}
		}
	}
	summary.QueueDepth = operationstate.QueueDepth(summary.Counts)
	return summary
}

func (s operationStatusSummary) reconcilerMap() map[string]any {
	return map[string]any{
		"enabled":              true,
		"queueDepth":           s.QueueDepth,
		"staleRunningCount":    s.StaleRunning,
		"staleThresholdSecond": s.StaleThresholdSeconds,
	}
}
