package handler

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

const catalogControllerOperationWindow = 1000

func (h *CatalogHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	all := !restricted || h.authz.engine == nil
	clusters := []uuid.UUID{}
	if !all {
		var allowed map[uuid.UUID]struct{}
		all, allowed, _ = h.authz.engine.AuthorizedScopeIDs(bindings, rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
		for _, id := range sortedUUIDs(allowed) {
			// Match the previous cluster-only summary permission semantics.
			if h.authz.allowsCluster(bindings, id, rbac.ResourceCatalog, rbac.VerbRead) {
				clusters = append(clusters, id)
			}
		}
	}
	pager, hasPager := h.queries.(catalogOperationPager)
	var ops []sqlc.CatalogOperation
	var total any
	scope := "global"
	if all {
		ops, err = h.queries.ListCatalogOperations(ctx, sqlc.ListCatalogOperationsParams{Limit: catalogControllerOperationWindow, Offset: 0})
		if err == nil && hasPager {
			total, err = pager.CountCatalogOperations(ctx, sqlc.CountCatalogOperationsParams{})
		}
	} else {
		scope = "authorized-clusters"
		if !hasPager {
			return nil, errors.New("scoped catalog controller summary is unavailable")
		}
		ops, err = pager.ListCatalogOperationsForScopes(ctx, sqlc.ListCatalogOperationsForScopesParams{ClusterIds: clusters, QueryLimit: catalogControllerOperationWindow, QueryOffset: 0})
		if err == nil {
			total, err = pager.CountCatalogOperationsForScopes(ctx, sqlc.CountCatalogOperationsForScopesParams{ClusterIds: clusters})
		}
	}
	if err != nil {
		return nil, err
	}
	if len(ops) > catalogControllerOperationWindow {
		return nil, errors.New("catalog controller summary exceeded its operation window")
	}
	if count, known := total.(int64); known && count < 0 {
		return nil, errors.New("catalog controller summary count is unavailable")
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.CatalogOperation]{
		Status:    func(op sqlc.CatalogOperation) string { return op.Status },
		CreatedAt: func(op sqlc.CatalogOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.CatalogOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.CatalogOperation) bool {
			if !restricted {
				return true
			}
			id, err := catalogOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, id, rbac.ResourceCatalog, rbac.VerbRead)
		},
		Preview:               func(ctx context.Context, op sqlc.CatalogOperation) map[string]any { return h.operationPreview(ctx, op) },
		StaleThresholdSeconds: 60,
	})
	charts, err := h.queries.CountHelmCharts(ctx)
	if err != nil {
		return nil, err
	}
	var installed any
	if !restricted {
		installed, err = h.queries.CountInstalledCharts(ctx)
		if err != nil {
			return nil, err
		}
	}
	returned := 0
	for _, count := range opSummary.Counts {
		returned += count
	}
	// List and count are separate reads. Equality cannot prove a complete
	// observation across concurrent writes; keep this recent window incomplete.
	complete := false
	notice := ""
	if !complete {
		notice = "Operation counts and queue depth summarize a bounded recent authorized window; complete totals are not represented by those fields."
	}
	return map[string]any{
		"reconciler":         opSummary.reconcilerMap(),
		"catalog":            map[string]any{"chartCount": charts, "installedCount": installed},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
		"operationSummary":   map[string]any{"scope": scope, "windowLimit": catalogControllerOperationWindow, "returned": returned, "total": total, "complete": complete, "notice": notice},
	}, nil
}
