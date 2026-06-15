package agentlifecycle

import "github.com/alphabravocompany/astronomer-go/internal/operationstate"

const (
	OperationTypeUpgrade = "agent_upgrade"
)

const (
	StatusPending   = operationstate.Pending
	StatusRunning   = operationstate.Running
	StatusSucceeded = "succeeded"
	StatusFailed    = operationstate.Failed
	StatusCancelled = "cancelled"
)

func IsTerminal(status string) bool {
	return status == StatusSucceeded || status == StatusFailed || status == StatusCancelled
}
