package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Chain multiple pgx.QueryTracer implementations.
//
// pgx accepts exactly one Tracer per ConnConfig, so the OTel tracer
// (spans) and the existing query tracer (Prometheus histograms) cannot
// both be plugged in directly. compositeQueryTracer wraps a list and
// calls TraceQueryStart on each in order (chaining the context the
// child returns), then TraceQueryEnd in reverse order so a child's
// "started_at" context value is still readable when its End fires.
//
// Only the QueryTracer interface is composed here. sqlc-generated code
// in this project never calls SendBatch or CopyFrom (verified at write
// time), so BatchTracer/CopyFromTracer composition is left out — if
// that changes, extend this type accordingly.

type compositeQueryTracer struct {
	tracers []pgx.QueryTracer
}

func newCompositeQueryTracer(t ...pgx.QueryTracer) *compositeQueryTracer {
	out := &compositeQueryTracer{tracers: make([]pgx.QueryTracer, 0, len(t))}
	for _, tr := range t {
		if tr != nil {
			out.tracers = append(out.tracers, tr)
		}
	}
	return out
}

func (c *compositeQueryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	for _, t := range c.tracers {
		ctx = t.TraceQueryStart(ctx, conn, data)
	}
	return ctx
}

func (c *compositeQueryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	// Reverse order so the *last* TraceQueryStart's context values are
	// available to the *first* TraceQueryEnd — symmetric with normal
	// defer-style cleanup.
	for i := len(c.tracers) - 1; i >= 0; i-- {
		c.tracers[i].TraceQueryEnd(ctx, conn, data)
	}
}
