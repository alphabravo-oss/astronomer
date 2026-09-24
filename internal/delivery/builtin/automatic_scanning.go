package builtin

import "github.com/alphabravocompany/astronomer-go/internal/registration"

// Automatic scanning on ready clusters does not reopen registration. The normal
// delivery target and rollout expose scanner progress, failures, and retries.
func (s registrationState) shouldReconcile() bool {
	if !s.inventoryReady {
		return false
	}
	switch s.phase {
	case registration.PhaseReady:
		return !s.isLocal && !s.scanningDisabled
	case registration.PhaseConnected, registration.PhaseProvisioning:
		return s.install
	default:
		return false
	}
}
