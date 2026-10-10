package handler

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
)

type controlPlaneAlertCursorQuerier interface {
	ListControlPlaneCursorAlertsAllFirst(context.Context, int32) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsAllAfter(context.Context, sqlc.ListControlPlaneCursorAlertsAllAfterParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsStatusFirst(context.Context, sqlc.ListControlPlaneCursorAlertsStatusFirstParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsStatusAfter(context.Context, sqlc.ListControlPlaneCursorAlertsStatusAfterParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsControllerFirst(context.Context, sqlc.ListControlPlaneCursorAlertsControllerFirstParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsControllerAfter(context.Context, sqlc.ListControlPlaneCursorAlertsControllerAfterParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsStatusControllerFirst(context.Context, sqlc.ListControlPlaneCursorAlertsStatusControllerFirstParams) ([]sqlc.ControlPlaneAlert, error)
	ListControlPlaneCursorAlertsStatusControllerAfter(context.Context, sqlc.ListControlPlaneCursorAlertsStatusControllerAfterParams) ([]sqlc.ControlPlaneAlert, error)
}

// These finite generated queries preserve direct equality predicates in both
// custom and generic plans. Values always remain bound parameters.
func listControlPlaneCursorAlerts(ctx context.Context, q controlPlaneAlertCursorQuerier, status, controller string, after *paging.Cursor, limit int32) ([]sqlc.ControlPlaneAlert, error) {
	if after == nil {
		return listControlPlaneCursorAlertsFirst(ctx, q, status, controller, limit)
	}
	return listControlPlaneCursorAlertsAfter(ctx, q, status, controller, after, limit)
}

func listControlPlaneCursorAlertsFirst(ctx context.Context, q controlPlaneAlertCursorQuerier, status, controller string, limit int32) ([]sqlc.ControlPlaneAlert, error) {
	if status != "" && controller != "" {
		return q.ListControlPlaneCursorAlertsStatusControllerFirst(ctx, sqlc.ListControlPlaneCursorAlertsStatusControllerFirstParams{Status: status, Controller: controller, QueryLimit: limit})
	}
	if status != "" {
		return q.ListControlPlaneCursorAlertsStatusFirst(ctx, sqlc.ListControlPlaneCursorAlertsStatusFirstParams{Status: status, QueryLimit: limit})
	}
	if controller != "" {
		return q.ListControlPlaneCursorAlertsControllerFirst(ctx, sqlc.ListControlPlaneCursorAlertsControllerFirstParams{Controller: controller, QueryLimit: limit})
	}
	return q.ListControlPlaneCursorAlertsAllFirst(ctx, limit)
}

func listControlPlaneCursorAlertsAfter(ctx context.Context, q controlPlaneAlertCursorQuerier, status, controller string, after *paging.Cursor, limit int32) ([]sqlc.ControlPlaneAlert, error) {
	if status != "" && controller != "" {
		return q.ListControlPlaneCursorAlertsStatusControllerAfter(ctx, sqlc.ListControlPlaneCursorAlertsStatusControllerAfterParams{Status: status, Controller: controller, AfterFiredAt: after.Time, AfterID: after.ID, QueryLimit: limit})
	}
	if status != "" {
		return q.ListControlPlaneCursorAlertsStatusAfter(ctx, sqlc.ListControlPlaneCursorAlertsStatusAfterParams{Status: status, AfterFiredAt: after.Time, AfterID: after.ID, QueryLimit: limit})
	}
	if controller != "" {
		return q.ListControlPlaneCursorAlertsControllerAfter(ctx, sqlc.ListControlPlaneCursorAlertsControllerAfterParams{Controller: controller, AfterFiredAt: after.Time, AfterID: after.ID, QueryLimit: limit})
	}
	return q.ListControlPlaneCursorAlertsAllAfter(ctx, sqlc.ListControlPlaneCursorAlertsAllAfterParams{AfterFiredAt: after.Time, AfterID: after.ID, QueryLimit: limit})
}
