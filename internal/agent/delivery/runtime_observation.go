package delivery

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// inspectInventory currently performs direct observation. Future cache-backed
// probes must choose their direct legacy path when negotiation is disabled;
// cached metadata is never silently stripped or restamped as a fresh read.
func (r *Runtime) inspectInventory(ctx context.Context) (protocol.DeliveryControllerInventory, Capabilities, error) {
	observedAt := r.now().UTC()
	inventory, capabilities, err := r.probe.Inspect(ctx)
	if err != nil {
		return inventory, capabilities, err
	}
	enabled := r.config.ObservationFreshness != nil && r.config.ObservationFreshness()
	if !enabled {
		if inventory.Observation != nil {
			return inventory, capabilities, errors.New("observation contract requires direct legacy probe")
		}
		for _, component := range inventory.SystemComponents {
			if component.Observation != nil {
				return inventory, capabilities, errors.New("component observation contract requires direct legacy probe")
			}
		}
		return inventory, capabilities, nil
	}
	if inventory.Observation == nil {
		inventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &observedAt}
		// Failed source reads establish unavailability, not a new observation.
		// Successfully returned optional component projections retain their own
		// direct-read timestamp; inaccessible optional kinds are omitted today.
		if strings.HasSuffix(inventory.CompatibilityMessage, "_unavailable") {
			inventory.Observation = &protocol.DeliveryObservation{State: protocol.ObservationUnavailable}
			inventory.Ready = false
		}
		inventory.SystemComponents = append([]protocol.SystemComponent(nil), inventory.SystemComponents...)
		for n := range inventory.SystemComponents {
			inventory.SystemComponents[n].Observation = &protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &observedAt}
		}
	}
	return inventory, capabilities, nil
}

// RetryObservation is invoked when an old-session observation frame cannot be
// sent to the newly negotiated peer. Wake the single runtime owner, invalidate
// status suppression, and release a waiting state request without waiting 30s.
func (r *Runtime) RetryObservation(message *protocol.Message) {
	r.resetObservation.Store(true)
	if message.Type == protocol.MsgDeliveryStateRequest {
		_, _ = r.HandleStateResponse(context.Background(), &protocol.Message{RequestID: message.RequestID, Error: "observation_contract_changed"})
	}
	_, _ = r.HandleReconcile(context.Background(), message)
}

func (r *Runtime) resetObservationSuppression() {
	if r.resetObservation.Swap(false) {
		r.lastStatusDigest = ""
		r.lastStatusSentAt = time.Time{}
	}
}
