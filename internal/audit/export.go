package audit

import "github.com/alphabravocompany/astronomer-go/internal/db/sqlc"

// ExportSpec is the immutable, persisted input to a durable audit CSV export.
// It contains no credentials and is safe to place in the task outbox.
type ExportSpec struct {
	Filter sqlc.AuditLogFilterParams `json:"filter"`
}
