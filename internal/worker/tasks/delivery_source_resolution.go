package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	deliverymetrics "github.com/alphabravocompany/astronomer-go/internal/delivery/metrics"
)

const (
	DeliverySourceResolutionType = "delivery:source-resolve"
	deliverySourceSweepLimit     = 16
)

type DeliverySourceResolver interface {
	ResolveOne(context.Context, uuid.UUID) error
	Sweep(context.Context, int) error
}

func NewDeliverySourceResolutionTask(resolutionID uuid.UUID) (*asynq.Task, error) {
	if resolutionID == uuid.Nil {
		return nil, errors.New("delivery source resolution task requires a resolution ID")
	}
	payload, err := json.Marshal(struct {
		ResolutionID uuid.UUID `json:"resolution_id"`
	}{resolutionID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(DeliverySourceResolutionType, payload), nil
}

func (runtime DeliveryRuntime) HandleDeliverySourceResolution(ctx context.Context, task *asynq.Task) (finalErr error) {
	defer func() {
		result := "success"
		if finalErr != nil {
			result = "failure"
		}
		deliverymetrics.ObserveWorker("source_resolver", result)
	}()
	if task == nil {
		return errors.New("delivery source resolution task is required")
	}
	resolver := runtime.SourceResolver
	if resolver == nil {
		return errors.New("delivery source resolver is not configured")
	}
	var payload struct {
		ResolutionID uuid.UUID `json:"resolution_id"`
	}
	if len(task.Payload()) != 0 && string(task.Payload()) != "{}" {
		decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return fmt.Errorf("decode delivery source resolution task: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return fmt.Errorf("decode delivery source resolution task: %w", err)
		}
	}
	if payload.ResolutionID == uuid.Nil {
		return resolver.Sweep(ctx, deliverySourceSweepLimit)
	}
	return resolver.ResolveOne(ctx, payload.ResolutionID)
}
