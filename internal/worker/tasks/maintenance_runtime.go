package tasks

import (
	"fmt"

	"github.com/hibiken/asynq"
)

// MaintenanceRuntime owns standalone worker tasks that repair or migrate
// durable control-plane state without requiring a member-cluster tunnel.
type MaintenanceRuntime struct {
	AgentTokens          AgentTokenRotateDeps
	PlaintextCredentials PlaintextCredentialMigrationDeps
}

func (runtime MaintenanceRuntime) Validate() error {
	return validateRequiredRuntimeDependencies("maintenance", []requiredRuntimeDependency{
		{name: "agent_tokens.queries", value: runtime.AgentTokens.Queries},
		{name: "plaintext_credentials.queries", value: runtime.PlaintextCredentials.Queries},
		{name: "plaintext_credentials.encryptor", value: runtime.PlaintextCredentials.Encryptor},
	})
}

func (runtime MaintenanceRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := runtime.Validate(); err != nil {
		return nil, fmt.Errorf("validate maintenance runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		AgentTokenRotateSweepType:        runtime.HandleAgentTokenRotateSweep,
		PlaintextCredentialMigrationType: runtime.HandlePlaintextCredentialMigration,
	}, nil
}

// CRDOwnershipRuntime is Kubernetes-dependent and therefore belongs to the
// server-embedded tunnel worker, never the standalone database/Redis worker.
type CRDOwnershipRuntime struct {
	Deps CRDOwnershipDriftDeps
}

func (runtime CRDOwnershipRuntime) HandlerBindings() (map[string]asynq.HandlerFunc, error) {
	if err := validateRequiredRuntimeDependencies("CRD ownership", []requiredRuntimeDependency{
		{name: "queries", value: runtime.Deps.Queries},
		{name: "dynamic_client", value: runtime.Deps.Dynamic},
	}); err != nil {
		return nil, fmt.Errorf("validate CRD ownership runtime: %w", err)
	}
	return map[string]asynq.HandlerFunc{
		CRDOwnershipDriftCheckType: runtime.HandleCRDOwnershipDriftCheck,
	}, nil
}
