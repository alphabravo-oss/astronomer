package systemrollout

import (
	"math"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

// systemReleaseSlots is the hard forward-rollout safety budget. A released or
// applying assignment reserves both a concurrency slot and an unavailable
// slot until it reports Ready. Failed assignments keep consuming availability,
// preventing a controller from compounding an outage below the configured
// failure threshold.
func systemReleaseSlots(strategy model.RolloutStrategy, total int32, current counts) int32 {
	if total <= 0 {
		return 0
	}
	// Widen before bounding/subtracting. Every uint32 budget and the product of
	// an int32 fleet size with a uint32 percentage fit in signed 64 bits.
	fleet := int64(total)
	reserved := int64(current.inFlight) + int64(current.immatureReady)
	concurrency := min(int64(strategy.MaxConcurrent), fleet) - reserved
	availabilityLimit := systemUnavailableBudget(strategy.MaxUnavailable, fleet)
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

// systemUnavailableBudget resolves max_unavailable for a system rollout. Every
// released assignment reserves availability until it reports Ready (there is
// no "already unavailable, release as repair" path as in application
// rollouts), so a budget that resolves to zero — count 0, or a percentage that
// floors to zero on a small fleet — would never release anything and the
// mandatory platform upgrade would stall forever. Zero therefore means the
// strictest serial rollout: one member at a time, and because failed members
// keep consuming the budget, the first failure halts forward progress.
func systemUnavailableBudget(amount model.Amount, fleet int64) int64 {
	limit := fleet
	switch amount.Type {
	case model.AmountCount:
		limit = min(int64(amount.Value), fleet)
	case model.AmountPercent:
		limit = fleet * int64(amount.Value) / 100
	}
	return max(limit, 1)
}
