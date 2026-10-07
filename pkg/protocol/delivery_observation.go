package protocol

import (
	"errors"
	"slices"
	"time"
)

// FeatureDeliveryObservation is negotiated for each tunnel session. Delivery
// decoders are strict, so agents must not emit these fields without an ACK.
const FeatureDeliveryObservation = "delivery-observation-v1"

// NegotiatedCapabilities returns only optional wire features supported by both
// peers. This is separate from the agent's privilege-dependent capability set.
func NegotiatedCapabilities(offered []string) []string {
	if slices.Contains(offered, FeatureDeliveryObservation) {
		return []string{FeatureDeliveryObservation}
	}
	return nil
}

type ObservationState string

const (
	ObservationCurrent      ObservationState = "current"
	ObservationStale        ObservationState = "stale"
	ObservationUnsynced     ObservationState = "unsynced"
	ObservationDenied       ObservationState = "denied"
	ObservationAbsent       ObservationState = "absent"
	ObservationDisconnected ObservationState = "disconnected"
	ObservationUnavailable  ObservationState = "unavailable"
)

// DeliveryObservation describes source observation, never message receipt.
// Never-observed unavailable kinds have a nil timestamp. The whole object is
// omitted for legacy peers, which continue direct observation/receipt semantics.
type DeliveryObservation struct {
	State      ObservationState `json:"state"`
	ObservedAt *time.Time       `json:"observed_at,omitempty"`
}

const MaxCurrentObservationAge = 5 * time.Minute
const MaxObservationClockSkew = 30 * time.Second

func (o *DeliveryObservation) validate(healthy bool, now time.Time) error {
	if o == nil {
		return nil
	}
	switch o.State {
	case ObservationCurrent, ObservationStale, ObservationAbsent:
		if o.ObservedAt == nil {
			return errors.New("observation state requires a source timestamp")
		}
	case ObservationUnsynced, ObservationDenied, ObservationDisconnected, ObservationUnavailable:
	default:
		return errors.New("unknown observation state")
	}
	if o.State != ObservationCurrent && healthy {
		return errors.New("noncurrent observation cannot report ready or healthy")
	}
	if o.ObservedAt != nil {
		if o.ObservedAt.IsZero() || o.ObservedAt.Year() < 1 || o.ObservedAt.Year() > 9999 || o.ObservedAt.After(now.Add(MaxObservationClockSkew)) {
			return errors.New("invalid observation timestamp")
		}
		if o.State == ObservationCurrent && now.Sub(*o.ObservedAt) > MaxCurrentObservationAge {
			return errors.New("current observation timestamp is stale")
		}
	}
	return nil
}

func (i DeliveryControllerInventory) validateObservations() error {
	now := time.Now().UTC()
	if err := i.Observation.validate(i.Ready, now); err != nil {
		return err
	}
	for _, component := range i.SystemComponents {
		if (i.Observation == nil) != (component.Observation == nil) {
			return errors.New("inventory and component observation contracts must agree")
		}
		if err := component.Observation.validate(component.Health == "healthy", now); err != nil {
			return err
		}
	}
	return nil
}

// WithoutObservationTimes canonicalizes only sample times, retaining state
// transitions. Copies nested observation objects so callers' snapshots remain
// immutable during digest computation.
func (i DeliveryControllerInventory) WithoutObservationTimes() DeliveryControllerInventory {
	i.Observation = withoutObservationTime(i.Observation)
	i.SystemComponents = append([]SystemComponent(nil), i.SystemComponents...)
	for n := range i.SystemComponents {
		i.SystemComponents[n].Observation = withoutObservationTime(i.SystemComponents[n].Observation)
	}
	return i
}
func withoutObservationTime(o *DeliveryObservation) *DeliveryObservation {
	if o == nil {
		return nil
	}
	return &DeliveryObservation{State: o.State}
}

// Mutation decisions do not claim source health. Their assessment timestamp
// remains required, but modern persistence must keep source time NULL when the
// observation object is absent. This exception preserves executor/tombstone
// semantics without labeling a local decision as a cache observation.
func (d DeliveryDeploymentStatusV2) validateObservation(modern bool, now time.Time) error {
	if d.Observation == nil {
		if !modern {
			return nil
		}
		mutation := d.Phase == "deleting" || d.Phase == "removed"
		if d.Phase == "failed" {
			switch d.ErrorCode {
			case "local_apply_failed", "local_inventory_failed", "local_prune_refused", "local_prune_failed", "local_deletion_failed":
				mutation = true
			}
		}
		if !mutation || len(d.Conditions) != 0 || d.Inventory.Ready != 0 || d.ObservedRevision != "" || d.ObservedDigest != "" {
			return errors.New("modern deployment status requires source observation")
		}
		return nil
	}
	if !modern {
		return errors.New("deployment observation requires modern inventory contract")
	}
	if err := d.Observation.validate(d.Phase == "ready" || d.Inventory.Ready > 0, now); err != nil {
		return err
	}
	if d.Observation.State != ObservationCurrent {
		if d.Phase != "unknown" || d.ErrorCode != "observation_"+string(d.Observation.State) || len(d.Conditions) != 0 || d.Inventory.Entries != 0 || d.Inventory.Ready != 0 || d.Inventory.Failed != 0 || len(d.Inventory.Resources) != 0 || d.ObservedRevision != "" || d.ObservedDigest != "" {
			return errors.New("noncurrent deployment observation must report unknown without cached health")
		}
	}
	return nil
}
