package charlie

import (
	"context"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type featureReader interface {
	BoolValue(context.Context, string, bool) bool
}

type activeConnectionReader interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
}

type ActivationState string

const (
	ActivationFeatureDisabled ActivationState = "feature_disabled"
	ActivationUnconfigured    ActivationState = "unconfigured"
	ActivationInstalling      ActivationState = "installing"
	ActivationEmergencyStop   ActivationState = "emergency_disabled"
	ActivationInactive        ActivationState = "inactive"
	ActivationReady           ActivationState = "ready"
)

type Activation struct {
	State        ActivationState
	Runnable     bool
	Configurable bool
	HealthOnly   bool
	Connection   sqlc.CharlieConnection
}

// EvaluateActivation is the single backend-owned switch for handlers,
// bridge/MCP listeners, worker registrations, schedulers, triggers, and agent
// installation reconciliation. Any missing dependency or read error is inert.
func EvaluateActivation(ctx context.Context, features featureReader, connections activeConnectionReader) Activation {
	if features == nil || !features.BoolValue(ctx, "feature.charlie", false) {
		return activationResult(Activation{State: ActivationFeatureDisabled})
	}
	if connections == nil {
		return activationResult(Activation{State: ActivationUnconfigured, HealthOnly: true})
	}
	connection, err := connections.GetActiveCharlieConnection(ctx)
	if err != nil {
		return activationResult(Activation{State: ActivationUnconfigured, HealthOnly: true})
	}
	if connection.EmergencyDisabled {
		// Emergency disable is an execution boundary, not a control-plane
		// transport boundary. Keep the narrowly scoped configuration channel
		// available for an active installation so the product can confirm remote
		// disable, settle streams, suspend the agent, and later perform the
		// explicit disabled-mode recovery handshake. Runtime sessions, evidence,
		// findings, triggers, and tools remain gated by Runnable=false.
		return activationResult(Activation{State: ActivationEmergencyStop, Configurable: connection.Active, HealthOnly: true, Connection: connection})
	}
	if connection.OnboardingState != "active" {
		return activationResult(Activation{State: ActivationInstalling, HealthOnly: true, Connection: connection})
	}
	if !connection.Active || connection.RequestedMode == string(ModeDisabled) || connection.VerifiedMode == string(ModeDisabled) {
		return activationResult(Activation{State: ActivationInactive, Configurable: connection.Active, HealthOnly: true, Connection: connection})
	}
	return activationResult(Activation{State: ActivationReady, Runnable: true, Configurable: true, HealthOnly: true, Connection: connection})
}

func activationResult(result Activation) Activation {
	observeActivation(result.State)
	mode := ModeDisabled
	if result.State == ActivationReady {
		mode = EffectiveMode(Mode(result.Connection.RequestedMode), Mode(result.Connection.VerifiedMode), result.Connection.EmergencyDisabled)
	}
	observeEffectiveMode(mode)
	observeModeDrift(Mode(result.Connection.RequestedMode), Mode(result.Connection.VerifiedMode), result.Connection.Active && !result.Connection.EmergencyDisabled)
	observeConnectionExpiries(result.Connection, time.Now().UTC())
	return result
}
