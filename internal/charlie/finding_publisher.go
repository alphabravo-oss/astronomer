package charlie

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// EventFindingPublisher bridges durable Charlie findings into Astronomer's
// existing cross-replica event bus. FindingService calls it only after the
// product-local row and resource disclosure have been committed.
type EventFindingPublisher struct{ bus *events.Bus }

func NewEventFindingPublisher(bus *events.Bus) *EventFindingPublisher {
	return &EventFindingPublisher{bus: bus}
}

func (p *EventFindingPublisher) PublishCharlieFinding(_ context.Context, alert FindingAlert) error {
	p.publish(alert)
	return nil
}

func (p *EventFindingPublisher) PublishCharlieFindingLifecycle(_ context.Context, alert FindingAlert) {
	p.publish(alert)
}

func (p *EventFindingPublisher) publish(alert FindingAlert) {
	if p == nil || p.bus == nil {
		return
	}
	events.PublishChanged(p.bus, "charlie_finding", "", alert.FindingID, map[string]any{
		"severity":      alert.Severity,
		"status":        alert.Status,
		"resource_type": alert.ResourceType,
		"resource_id":   alert.ResourceID,
		"block_code":    alert.BlockCode,
		"repeat_count":  alert.RepeatCount,
		"deep_link":     "/dashboard/charlie?tab=findings&finding=" + alert.FindingID,
	})
}
