package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
// The application also uses connection establishment, prepared statements,
// SendBatch, and pool acquisition. Forward those optional interfaces only to
// children that implement them. CopyFrom is not used by the application.

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

func (c *compositeQueryTracer) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	for _, child := range c.tracers {
		if tracer, ok := child.(pgx.ConnectTracer); ok {
			ctx = tracer.TraceConnectStart(ctx, data)
		}
	}
	return ctx
}

func (c *compositeQueryTracer) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	for i := len(c.tracers) - 1; i >= 0; i-- {
		if tracer, ok := c.tracers[i].(pgx.ConnectTracer); ok {
			tracer.TraceConnectEnd(ctx, data)
		}
	}
}

func (c *compositeQueryTracer) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	for _, child := range c.tracers {
		if tracer, ok := child.(pgx.PrepareTracer); ok {
			ctx = tracer.TracePrepareStart(ctx, conn, data)
		}
	}
	return ctx
}

func (c *compositeQueryTracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	for i := len(c.tracers) - 1; i >= 0; i-- {
		if tracer, ok := c.tracers[i].(pgx.PrepareTracer); ok {
			tracer.TracePrepareEnd(ctx, conn, data)
		}
	}
}

func (c *compositeQueryTracer) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	for _, child := range c.tracers {
		if tracer, ok := child.(pgx.BatchTracer); ok {
			ctx = tracer.TraceBatchStart(ctx, conn, data)
		}
	}
	return ctx
}

func (c *compositeQueryTracer) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	for i := len(c.tracers) - 1; i >= 0; i-- {
		if tracer, ok := c.tracers[i].(pgx.BatchTracer); ok {
			tracer.TraceBatchEnd(ctx, conn, data)
		}
	}
}

func (c *compositeQueryTracer) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireStartData) context.Context {
	for _, child := range c.tracers {
		if tracer, ok := child.(pgxpool.AcquireTracer); ok {
			ctx = tracer.TraceAcquireStart(ctx, pool, data)
		}
	}
	return ctx
}

func (c *compositeQueryTracer) TraceAcquireEnd(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	for i := len(c.tracers) - 1; i >= 0; i-- {
		if tracer, ok := c.tracers[i].(pgxpool.AcquireTracer); ok {
			tracer.TraceAcquireEnd(ctx, pool, data)
		}
	}
}

func (c *compositeQueryTracer) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	for _, child := range c.tracers {
		if tracer, ok := child.(pgx.BatchTracer); ok {
			tracer.TraceBatchQuery(ctx, conn, data)
		}
	}
}
