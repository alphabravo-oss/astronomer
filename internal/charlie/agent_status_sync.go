package charlie

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

type agentStatusWriter interface {
	UpdateCharlieAgentStatus(context.Context, sqlc.UpdateCharlieAgentStatusParams) (sqlc.CharlieConnection, error)
}

// syncAgentStatus persists only bounded bridge health and election metadata.
// It never treats an absent/invalid leader as epoch zero, which would weaken
// the product-side fencing check after a transient bridge failure.
func syncAgentStatus(ctx context.Context, writer agentStatusWriter, connection sqlc.CharlieConnection, status AdminBridgeStatus, now time.Time) (sqlc.CharlieConnection, error) {
	if writer == nil || !connection.Active || status.Epoch < 1 || strings.TrimSpace(status.LeaderInstanceID) == "" {
		return sqlc.CharlieConnection{}, fmt.Errorf("Charlie agent leadership is unavailable")
	}
	health := "degraded"
	lastError := "bridge_status_degraded"
	connectedAt := pgtype.Timestamptz{}
	if status.CentralHealth == "healthy" {
		health = "ready"
		lastError = ""
		connectedAt = pgtype.Timestamptz{Time: now.UTC(), Valid: true}
	}
	return writer.UpdateCharlieAgentStatus(ctx, sqlc.UpdateCharlieAgentStatusParams{
		LeaderInstanceID: strings.TrimSpace(status.LeaderInstanceID), FencingEpoch: status.Epoch,
		HealthState: health, LastConnectedAt: connectedAt, LastErrorCode: lastError, ID: connection.ID,
	})
}
