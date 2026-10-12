package db

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type traceContextKey struct{ phase, name string }
type optionalTraceProbe struct {
	t        *testing.T
	name     string
	calls    *[]string
	prior    bool
	payloads *[]any
}

func (p *optionalTraceProbe) payload(data any) {
	if p.payloads != nil {
		*p.payloads = append(*p.payloads, data)
	}
}
func (p *optionalTraceProbe) start(ctx context.Context, phase string) context.Context {
	if p.prior && ctx.Value(traceContextKey{phase, "first"}) != "first" {
		p.t.Errorf("%s start lost prior child context", phase)
	}
	*p.calls = append(*p.calls, p.name+":"+phase+":start")
	return context.WithValue(ctx, traceContextKey{phase, p.name}, p.name)
}
func (p *optionalTraceProbe) end(ctx context.Context, phase string) {
	for _, name := range []string{"first", "last"} {
		if ctx.Value(traceContextKey{phase, name}) != name {
			p.t.Errorf("%s callback lost %s context", phase, name)
		}
	}
	*p.calls = append(*p.calls, p.name+":"+phase+":end")
}
func (p *optionalTraceProbe) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	p.payload(data)
	return p.start(ctx, "Query")
}
func (p *optionalTraceProbe) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	p.payload(data)
	p.end(ctx, "Query")
}
func (p *optionalTraceProbe) TraceConnectStart(ctx context.Context, data pgx.TraceConnectStartData) context.Context {
	p.payload(data)
	return p.start(ctx, "Connect")
}
func (p *optionalTraceProbe) TraceConnectEnd(ctx context.Context, data pgx.TraceConnectEndData) {
	p.payload(data)
	p.end(ctx, "Connect")
}
func (p *optionalTraceProbe) TracePrepareStart(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	p.payload(data)
	return p.start(ctx, "Prepare")
}
func (p *optionalTraceProbe) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	p.payload(data)
	p.end(ctx, "Prepare")
}
func (p *optionalTraceProbe) TraceBatchStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchStartData) context.Context {
	p.payload(data)
	return p.start(ctx, "Batch")
}
func (p *optionalTraceProbe) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchEndData) {
	p.payload(data)
	p.end(ctx, "Batch")
}
func (p *optionalTraceProbe) TraceAcquireStart(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireStartData) context.Context {
	p.payload(data)
	return p.start(ctx, "Acquire")
}
func (p *optionalTraceProbe) TraceAcquireEnd(ctx context.Context, pool *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	p.payload(data)
	p.end(ctx, "Acquire")
}
func (p *optionalTraceProbe) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, data pgx.TraceBatchQueryData) {
	for _, name := range []string{"first", "last"} {
		if ctx.Value(traceContextKey{"Batch", name}) != name {
			p.t.Errorf("batch query lost %s context", name)
		}
	}
	if data.SQL != "SELECT $1" || len(data.Args) != 1 || data.Args[0] != 42 {
		p.t.Error("batch query payload was not preserved")
	}
	*p.calls = append(*p.calls, p.name+":Batch:query")
}

type queryOnlyProbe struct{}

func (*queryOnlyProbe) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (*queryOnlyProbe) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func traceFixture(t *testing.T) (*compositeQueryTracer, *[]string) {
	calls := []string{}
	return newCompositeQueryTracer(nil, &optionalTraceProbe{t: t, name: "first", calls: &calls}, &queryOnlyProbe{}, &optionalTraceProbe{t: t, name: "last", calls: &calls, prior: true}), &calls
}
func assertTraceOrder(t *testing.T, calls []string, phase string) {
	t.Helper()
	want := []string{"first:" + phase + ":start", "last:" + phase + ":start"}
	if phase == "Batch" {
		want = append(want, "first:Batch:query", "last:Batch:query")
	}
	want = append(want, "last:"+phase+":end", "first:"+phase+":end")
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("trace callbacks: got%v want%v", calls, want)
	}
}
func TestCompositeQueryCallbacksPreserveContextAndOrder(t *testing.T) {
	chain, calls := traceFixture(t)
	tracer := chain
	ctx := tracer.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{})
	assertTraceOrder(t, *calls, "Query")
}
func TestCompositeConnectCallbacksPreserveContextAndOrder(t *testing.T) {
	chain, calls := traceFixture(t)
	tracer := func() pgx.ConnectTracer {
		v, ok := any(chain).(pgx.ConnectTracer)
		if !ok {
			t.Fatal("composite discards used ConnectTracer")
		}
		return v
	}()
	ctx := tracer.TraceConnectStart(context.Background(), pgx.TraceConnectStartData{})
	tracer.TraceConnectEnd(ctx, pgx.TraceConnectEndData{})
	assertTraceOrder(t, *calls, "Connect")
}
func TestCompositePrepareCallbacksPreserveContextAndOrder(t *testing.T) {
	chain, calls := traceFixture(t)
	tracer := func() pgx.PrepareTracer {
		v, ok := any(chain).(pgx.PrepareTracer)
		if !ok {
			t.Fatal("composite discards used PrepareTracer")
		}
		return v
	}()
	ctx := tracer.TracePrepareStart(context.Background(), nil, pgx.TracePrepareStartData{})
	tracer.TracePrepareEnd(ctx, nil, pgx.TracePrepareEndData{})
	assertTraceOrder(t, *calls, "Prepare")
}
func TestCompositeBatchCallbacksPreserveContextAndOrder(t *testing.T) {
	chain, calls := traceFixture(t)
	tracer := func() pgx.BatchTracer {
		v, ok := any(chain).(pgx.BatchTracer)
		if !ok {
			t.Fatal("composite discards used BatchTracer")
		}
		return v
	}()
	ctx := tracer.TraceBatchStart(context.Background(), nil, pgx.TraceBatchStartData{})
	tracer.TraceBatchQuery(ctx, nil, pgx.TraceBatchQueryData{SQL: "SELECT $1", Args: []any{42}})
	tracer.TraceBatchEnd(ctx, nil, pgx.TraceBatchEndData{})
	assertTraceOrder(t, *calls, "Batch")
}
func TestCompositeAcquireCallbacksPreserveContextAndOrder(t *testing.T) {
	chain, calls := traceFixture(t)
	tracer := func() pgxpool.AcquireTracer {
		v, ok := any(chain).(pgxpool.AcquireTracer)
		if !ok {
			t.Fatal("composite discards used AcquireTracer")
		}
		return v
	}()
	ctx := tracer.TraceAcquireStart(context.Background(), nil, pgxpool.TraceAcquireStartData{})
	tracer.TraceAcquireEnd(ctx, nil, pgxpool.TraceAcquireEndData{})
	assertTraceOrder(t, *calls, "Acquire")
}
func TestCompositeDoesNotClaimUnusedOptionalInterfaces(t *testing.T) {
	chain, _ := traceFixture(t)
	if _, ok := any(chain).(pgx.CopyFromTracer); ok {
		t.Error("no application CopyFrom path was inventoried")
	}
	if _, ok := any(chain).(pgxpool.ReleaseTracer); ok {
		t.Error("selected children do not implement ReleaseTracer")
	}
}

func TestCompositePreservesOptionalCallbackPayloads(t *testing.T) {
	chain, _ := traceFixture(t)
	var received []any
	for _, child := range chain.tracers {
		if probe, ok := child.(*optionalTraceProbe); ok {
			probe.payloads = &received
		}
	}
	ctx := context.Background()
	conn, config, batch := new(pgx.Conn), new(pgx.ConnConfig), new(pgx.Batch)
	failure := errors.New("synthetic callback failure")
	connectStart, connectEnd := pgx.TraceConnectStartData{ConnConfig: config}, pgx.TraceConnectEndData{Conn: conn, Err: failure}
	prepareStart, prepareEnd := pgx.TracePrepareStartData{Name: "prepared", SQL: "SELECT $1"}, pgx.TracePrepareEndData{AlreadyPrepared: true, Err: failure}
	batchStart, batchEnd := pgx.TraceBatchStartData{Batch: batch}, pgx.TraceBatchEndData{Err: failure}
	acquireStart, acquireEnd := pgxpool.TraceAcquireStartData{}, pgxpool.TraceAcquireEndData{Conn: conn, Err: failure}
	chain.TraceConnectEnd(chain.TraceConnectStart(ctx, connectStart), connectEnd)
	chain.TracePrepareEnd(chain.TracePrepareStart(ctx, conn, prepareStart), conn, prepareEnd)
	chain.TraceBatchEnd(chain.TraceBatchStart(ctx, conn, batchStart), conn, batchEnd)
	chain.TraceAcquireEnd(chain.TraceAcquireStart(ctx, nil, acquireStart), nil, acquireEnd)
	want := []any{}
	for _, pair := range [][2]any{{connectStart, connectEnd}, {prepareStart, prepareEnd}, {batchStart, batchEnd}, {acquireStart, acquireEnd}} {
		want = append(want, pair[0], pair[0], pair[1], pair[1])
	}
	if !reflect.DeepEqual(received, want) {
		t.Fatal("optional callback metadata or error identity changed")
	}
}
