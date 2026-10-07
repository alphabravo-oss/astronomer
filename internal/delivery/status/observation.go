package status

import (
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/jackc/pgx/v5/pgtype"
)

// Legacy agents have no source freshness contract: retain documented receipt
// time semantics. Negotiated observations preserve their source time even on
// semantically coalesced messages. Never-observed kinds remain SQL NULL.
func inventoryObservedAt(inventory protocol.DeliveryControllerInventory, receivedAt time.Time) pgtype.Timestamptz {
	if inventory.Observation == nil {
		return timestamp(receivedAt)
	}
	if inventory.Observation.ObservedAt == nil {
		return pgtype.Timestamptz{}
	}
	return timestamp(inventory.Observation.ObservedAt.UTC())
}
