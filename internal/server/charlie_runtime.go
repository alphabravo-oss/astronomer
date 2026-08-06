package server

import (
	"context"
	"sync"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

// charlieRuntimeGeneration is the dynamically replaceable product work plane.
// The HTTP services remain gate-checked and dormant; only this generation owns
// listeners, event consumers, trigger registration, and capability clients.
type charlieRuntimeGeneration struct {
	mcp        *charlie.MCPRuntime
	events     *charlie.EventRuntime
	dispatcher tasks.CharlieTriggerDispatcher
	inspector  *asynq.Inspector

	mu      sync.Mutex
	stopped bool
}

func (g *charlieRuntimeGeneration) Run(ctx context.Context) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return
	}
	tasks.ConfigureCharlieTriggerDispatcher(g.dispatcher)
	g.mu.Unlock()
	if g.events != nil {
		go g.events.Run(ctx)
	}
	if g.mcp != nil {
		_ = g.mcp.Run(ctx)
		return
	}
	<-ctx.Done()
}

func (g *charlieRuntimeGeneration) Shutdown(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		return nil
	}
	g.stopped = true
	tasks.ConfigureCharlieTriggerDispatcher(nil)
	g.mu.Unlock()
	var err error
	if g.mcp != nil {
		err = g.mcp.Shutdown(ctx)
	}
	if g.inspector != nil {
		_ = g.inspector.Close()
	}
	return err
}
