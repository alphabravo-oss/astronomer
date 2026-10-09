# System rollout budget bounds

Before the fix, a valid v1 max_concurrent or count budget above MaxInt32 narrowed
to a negative int32 before min(total). The new regression reproduces zero release
slots for a three-cluster fleet at MaxInt32+1 and MaxUint32. One reservation also
wrapped into 2147483647 slots.

The scheduler now widens uint32 budgets and int32 counts to int64 before bounding,
percentage multiplication and subtraction. Nonpositive fleet sizes produce zero
slots. A positive result has an explicit MaxInt32 guard before narrowing. The
product of the largest nonnegative int32 fleet and uint32 amount fits in int64;
canonical strategy validation separately restricts percentage values to 100.
Large v1 budgets still mean the available fleet size rather than becoming a new
validation rejection.

`go test -p=2 ./internal/delivery/systemrollout -count=1`, narrow go vet, and pinned
golangci-lint v2.14.0 on that package pass (0 issues). Regression output includes
large budgets, reservations, empty/negative fleets and subtraction of large
counts; existing normal count/percentage cases remain in the full package run.

Moving this small scheduler function into scheduling_budget.go reduces the
previously oversized service.go below 800 lines. The exact ratchet command is
`node scripts/check-complexity-budget.mjs --write-baseline`; its only baseline
change removes the service.go entry (801 lines). The resulting budget gate
passes with 41 file ceilings and 65 function ceilings. No ignore was added.
