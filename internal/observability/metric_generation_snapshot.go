package observability

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type MetricGenerationBegin func(context.Context, pgx.TxOptions) (pgx.Tx, error)

var errGenerationBudget = errors.New("metric generation owner budget exceeded")

// One family, at most21queries/10k rows, in a committed read-only RR snapshot.
// No partial/failing/expired observation reaches the retirement registry.
func CollectMetricGenerationSnapshot(ctx context.Context, begin MetricGenerationBegin, family MetricOwnerFamily) ([]MetricGenerationRow, error) {
	if ctx == nil || begin == nil || !family.valid() {
		return nil, errors.New("bound metric generation observation required")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := begin(bounded, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(bounded), 2*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	queries := sqlc.New(tx)
	const pageSize int32 = 500
	var complete []MetricGenerationRow
	for offset := int32(0); offset <= MetricOwnerMax; offset += pageSize {
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		rows, err := queries.ListMetricOwnerGenerationsPage(bounded, sqlc.ListMetricOwnerGenerationsPageParams{Family: string(family), QueryLimit: pageSize, QueryOffset: offset})
		if err != nil {
			return nil, err
		}
		if err := bounded.Err(); err != nil {
			return nil, err
		}
		if len(rows) > int(pageSize) || len(complete)+len(rows) > MetricOwnerMax {
			return nil, errGenerationBudget
		}
		for _, row := range rows {
			complete = append(complete, MetricGenerationRow{ID: row.OwnerID, Generation: row.Generation})
		}
		if len(rows) < int(pageSize) {
			break
		}
	}
	if err := tx.Commit(bounded); err != nil {
		return nil, err
	}
	if err := bounded.Err(); err != nil {
		return nil, err
	}
	return complete, nil
}

// A supervised caller owns this serial loop and joins it during shutdown. The
// fixed family list is copied; a slow collection cannot launch an overlap.
func RunMetricGenerationCollector(ctx context.Context, begin MetricGenerationBegin, families []MetricOwnerFamily) {
	if ctx == nil || begin == nil {
		return
	}
	selected := append([]MetricOwnerFamily(nil), families...)
	seen := map[MetricOwnerFamily]bool{}
	for _, family := range selected {
		if !family.valid() || seen[family] {
			return
		}
		seen[family] = true
	}
	collect := func() {
		for _, family := range selected {
			if ctx.Err() != nil {
				return
			}
			started := time.Now().UTC()
			lookup := BeginMetricGenerationLookup(family)
			rows, err := CollectMetricGenerationSnapshot(ctx, begin, family)
			if err != nil || ctx.Err() != nil {
				result := "failed"
				if errors.Is(err, errGenerationBudget) {
					result = "budget_dropped"
				}
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					result = "canceled"
				}
				generationResult(lookup.instance, family, result)
				continue
			}
			if !lookup.Publish(rows, started) {
				generationResult(lookup.instance, family, "invalid")
			}
		}
	}
	collect()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}
