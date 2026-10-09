package systemrollout

import (
	"math"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func systemReleaseSlots(strategy model.RolloutStrategy, total int32, current counts) int32 {
	if total <= 0 {
		return 0
	}
	// Widen before bounding/subtracting. Every uint32 budget and the product of
	// an int32 fleet size with a uint32 percentage fit in signed 64 bits.
	fleet := int64(total)
	reserved := int64(current.inFlight) + int64(current.immatureReady)
	concurrency := min(int64(strategy.MaxConcurrent), fleet) - reserved
	availabilityLimit := fleet
	switch strategy.MaxUnavailable.Type {
	case model.AmountCount:
		availabilityLimit = min(int64(strategy.MaxUnavailable.Value), fleet)
	case model.AmountPercent:
		availabilityLimit = fleet * int64(strategy.MaxUnavailable.Value) / 100
	}
	slots := min(concurrency, availabilityLimit-reserved-int64(current.failed))
	if slots <= 0 {
		return 0
	}
	// The concurrency cap already bounds slots by total; retain an explicit
	// narrowing guard so future arithmetic changes cannot wrap a database count.
	if slots > math.MaxInt32 {
		return total
	}
	return int32(slots)
}
