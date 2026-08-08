package charlie

// Availability separates product feature availability from integration
// activation. Runtime and product evidence are allowed only in Active.
type Availability string

const (
	AvailabilityUnavailable       Availability = "feature_unavailable"
	AvailabilityAvailableInactive Availability = "available_inactive"
	AvailabilityActive            Availability = "active"
)

// ResolveAvailability is fail closed: an active connection cannot override a
// disabled feature, and an enabled feature does not imply activation.
func ResolveAvailability(featureEnabled, connectionActive bool) Availability {
	if !featureEnabled {
		return AvailabilityUnavailable
	}
	if !connectionActive {
		return AvailabilityAvailableInactive
	}
	return AvailabilityActive
}

// AllowsConfiguration permits onboarding only when the optional product
// feature exists. It does not permit sessions, findings, workers, or evidence.
func (a Availability) AllowsConfiguration() bool {
	return a == AvailabilityAvailableInactive || a == AvailabilityActive
}

// AllowsRuntime is the common foundation for handlers, workers, schedulers,
// launchers, and the local bridge client. Later phases must also apply live
// authority policy before doing work.
func (a Availability) AllowsRuntime() bool { return a == AvailabilityActive }

// AllowsEvidence follows runtime activation so neither pre-activation state
// can disclose product evidence.
func (a Availability) AllowsEvidence() bool { return a == AvailabilityActive }
