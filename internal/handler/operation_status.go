package handler

import "github.com/alphabravocompany/astronomer-go/internal/operationstate"

// OperationStatus values stored in the *_operations tables. Reconcilers
// and dispatchers read/write these via the constants so a typo can't
// silently break a state transition.
//
// These constants intentionally use plain `string` (no typed alias)
// because sqlc generates `Status string` directly on the operation
// row structs and on the *Params types. A typed alias would force a
// cast at every call site for zero added safety, so we keep the
// constants string-typed.
//
// Scope: only the *_operations tables (argocd_operations,
// catalog_operations, logging_operations, monitoring_operations,
// tool_operations, workload_operations). Other tables (backups,
// restore_operations, cluster_decommissions, security_policies,
// cis_scans, helm releases) have their own status alphabets that
// happen to overlap on some strings — do NOT swap those call sites to
// these constants.
const (
	OpStatusPending    = operationstate.Pending
	OpStatusRunning    = operationstate.Running
	OpStatusCompleted  = operationstate.Completed
	OpStatusFailed     = operationstate.Failed
	OpStatusSuperseded = operationstate.Superseded
)
