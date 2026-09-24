package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/catalogapp"
	"github.com/google/uuid"
)

type catalogRolloutStatusReader interface {
	RolloutStatus(context.Context, uuid.UUID, uuid.UUID) (catalogapp.Status, error)
}

func catalogOperationRollout(events []map[string]any) (uuid.UUID, uuid.UUID, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i]["stage"] != "rollout" {
			continue
		}
		detail, ok := events[i]["detail"].(map[string]any)
		if !ok {
			continue
		}
		target, _ := detail["targetId"].(string)
		rollout, _ := detail["rolloutId"].(string)
		targetID, e1 := uuid.Parse(target)
		rolloutID, e2 := uuid.Parse(rollout)
		if e1 == nil && e2 == nil && targetID != uuid.Nil && rolloutID != uuid.Nil {
			return targetID, rolloutID, true
		}
	}
	return uuid.Nil, uuid.Nil, false
}

// enrichCatalogOperationDeliveryStatus projects the asynchronous Flux
// workload outcome without holding a catalog worker claim for the rollout's
// full convergence deadline.
func (h *CatalogHandler) enrichCatalogOperationDeliveryStatus(ctx context.Context, op sqlc.CatalogOperation, resp map[string]any) {
	if op.TargetType != "installed_chart" || op.Status != "completed" {
		return
	}
	resp["journalStatus"] = op.Status
	resp["deliveryPhase"] = "unknown"
	resp["deliveryObservationError"] = "No durable workload outcome is available for this receipt"
	status, targetID, rolloutID, err := h.catalogOperationDeliveryObservation(ctx, op, resp)
	if err != nil {
		resp["deliveryObservationError"] = err.Error()
		return
	}
	resp["deliveryObservationError"] = ""
	resp["deliveryObservedAt"] = time.Now().UTC().Format(time.RFC3339)
	events, _ := resp["events"].([]map[string]any)
	phase := strings.TrimSpace(status.Phase)
	if phase == "" {
		phase = "pending"
	}
	resp["deliveryPhase"] = phase
	level, message, terminal := "info", "Flux is reconciling the application", false
	switch phase {
	case "ready":
		resp["status"], message, terminal = "completed", "Flux reports the application workloads ready", true
	case "removed":
		resp["status"], message, terminal = "completed", "Flux reports the application removed", true
	case "failed", "timed_out", "rollback_failed":
		resp["status"], level, message, terminal = "failed", "error", "Flux could not converge the application", true
	case "degraded":
		resp["status"], level, message = "running", "warn", "Flux reports degraded workloads and is continuing remediation"
	default:
		resp["status"] = "running"
	}
	events = append(events, map[string]any{
		"id": "delivery-" + phase, "level": level, "stage": "workloads", "message": message,
		"detail":    map[string]any{"phase": phase, "errorCode": status.LastErrorCode, "terminal": terminal, "targetId": targetID.String(), "rolloutId": rolloutID.String()},
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	})
	resp["events"] = events
}

type catalogDeletionStatusReader interface {
	DeletionStatus(context.Context, uuid.UUID) (catalogapp.Status, error)
}

func (h *CatalogHandler) catalogOperationDeliveryObservation(ctx context.Context, op sqlc.CatalogOperation, resp map[string]any) (catalogapp.Status, uuid.UUID, uuid.UUID, error) {
	events, _ := resp["events"].([]map[string]any)
	if op.OperationType == "uninstall" {
		reader, ok := h.delivery.(catalogDeletionStatusReader)
		if !ok {
			return catalogapp.Status{}, uuid.Nil, uuid.Nil, errors.New("Deletion tracking unavailable")
		}
		for i := len(events) - 1; i >= 0; i-- {
			if events[i]["stage"] != "deletion" {
				continue
			}
			detail, _ := events[i]["detail"].(map[string]any)
			raw, _ := detail["targetId"].(string)
			targetID, err := uuid.Parse(raw)
			if err != nil || targetID == uuid.Nil {
				continue
			}
			status, err := reader.DeletionStatus(ctx, targetID)
			if err != nil {
				return catalogapp.Status{}, targetID, uuid.Nil, errors.New("Could not observe target deletion status")
			}
			return status, targetID, uuid.Nil, nil
		}
		return catalogapp.Status{}, uuid.Nil, uuid.Nil, errors.New("No durable deletion identity is available for this receipt")
	}
	reader, ok := h.delivery.(catalogRolloutStatusReader)
	if !ok {
		return catalogapp.Status{}, uuid.Nil, uuid.Nil, errors.New("Rollout tracking unavailable")
	}
	targetID, rolloutID, ok := catalogOperationRollout(events)
	if !ok {
		return catalogapp.Status{}, uuid.Nil, uuid.Nil, errors.New("No durable rollout identity is available for this receipt")
	}
	status, err := reader.RolloutStatus(ctx, targetID, rolloutID)
	if err != nil {
		return catalogapp.Status{}, targetID, rolloutID, errors.New("Could not observe rollout status")
	}
	return status, targetID, rolloutID, nil
}
